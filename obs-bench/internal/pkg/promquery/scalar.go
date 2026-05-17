package promquery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
)

var jobNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_:.-]+$`)

// ValidateJobLabel проверяет строку перед подстановкой в PromQL (label value).
func ValidateJobLabel(name string) error {
	if name == "" {
		return fmt.Errorf("empty job name")
	}
	if !jobNamePattern.MatchString(name) {
		return fmt.Errorf("invalid job name for PromQL: %q", name)
	}
	return nil
}

type instantQueryResponse struct {
	Data struct {
		Result []struct {
			Value [2]any `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// QueryInstantScalar выполняет instant query к Prometheus-совместимому API и возвращает первое скалярное значение.
func QueryInstantScalar(ctx context.Context, client *http.Client, baseURL, promql string) (float64, error) {
	if client == nil {
		client = http.DefaultClient
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return 0, fmt.Errorf("parse base URL: %w", err)
	}
	u.Path = "/api/v1/query"
	q := u.Query()
	q.Set("query", promql)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("prometheus query %q: %w", promql, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return 0, fmt.Errorf("prometheus query %q: status %d, read body: %w", promql, resp.StatusCode, readErr)
		}
		return 0, fmt.Errorf("prometheus query %q: status %d: %s", promql, resp.StatusCode, body)
	}

	var result instantQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Data.Result) == 0 {
		return 0, fmt.Errorf("no data for query %q", promql)
	}

	raw, ok := result.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("unexpected value type")
	}

	return strconv.ParseFloat(raw, 64)
}
