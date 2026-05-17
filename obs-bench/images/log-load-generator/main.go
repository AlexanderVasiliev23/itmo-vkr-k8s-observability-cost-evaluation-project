package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

func main() {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("BACKEND")))
	lpsStr := strings.TrimSpace(os.Getenv("LOGS_PER_SEC"))
	lps, err := strconv.Atoi(lpsStr)
	if err != nil || lps < 1 {
		log.Fatalf("LOGS_PER_SEC must be a positive integer, got %q", lpsStr)
	}
	log.Printf("log-load-generator backend=%s logs_per_sec=%d", backend, lps)

	for {
		start := time.Now()
		deadline := start.Add(time.Second)
		sent := 0
		for sent < lps && time.Now().Before(deadline) {
			batch := lps / 10
			if batch < 1 {
				batch = 1
			}
			if batch > 500 {
				batch = 500
			}
			if sent+batch > lps {
				batch = lps - sent
			}
			var err error
			switch backend {
			case "loki":
				err = flushLoki(batch)
			case "opensearch":
				err = flushOpenSearch(batch)
			default:
				log.Fatalf("unknown BACKEND %q (want loki or opensearch)", backend)
			}
			if err != nil {
				log.Printf("send batch: %v", err)
				time.Sleep(50 * time.Millisecond)
				continue
			}
			sent += batch
		}
		if d := time.Until(deadline); d > 0 {
			time.Sleep(d)
		}
	}
}

type lokiPush struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

func flushLoki(n int) error {
	url := strings.TrimSpace(os.Getenv("LOKI_PUSH_URL"))
	if url == "" {
		return fmt.Errorf("LOKI_PUSH_URL empty")
	}
	now := time.Now().UnixNano()
	values := make([][]string, 0, n)
	for i := 0; i < n; i++ {
		ts := strconv.FormatInt(now+int64(i), 10)
		line := fmt.Sprintf("bench msg=%d t=%s", i, time.Now().UTC().Format(time.RFC3339Nano))
		values = append(values, []string{ts, line})
	}
	body, err := json.Marshal(lokiPush{
		Streams: []lokiStream{
			{
				Stream: map[string]string{"job": "bench", "app": "bench"},
				Values: values,
			},
		},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("loki push %d: %s", resp.StatusCode, string(b))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func flushOpenSearch(n int) error {
	base := strings.TrimSpace(os.Getenv("OPENSEARCH_BASE_URL"))
	if base == "" {
		return fmt.Errorf("OPENSEARCH_BASE_URL empty")
	}
	base = strings.TrimRight(base, "/")

	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		buf.WriteString(`{"index":{"_index":"logbench"}}` + "\n")
		doc := fmt.Sprintf(`{"msg":"bench","i":%d,"ts":"%s"}`, i, time.Now().UTC().Format(time.RFC3339Nano))
		buf.WriteString(doc + "\n")
	}
	req, err := http.NewRequest(http.MethodPost, base+"/_bulk", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("opensearch bulk %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		Errors bool `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.Errors {
		log.Printf("opensearch bulk: some documents failed to index")
	}
	return nil
}
