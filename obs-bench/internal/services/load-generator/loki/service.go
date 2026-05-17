package loki_load_generator_service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"obs-bench/internal/pkg/portforwardhttp"
	load_generator_service "obs-bench/internal/services/load-generator"
)

var _ load_generator_service.IStackLoadGenerator = &service{}

// 3 типа LogQL-запросов (лог-стрим, rate, count_over_time). QPS=1 — константа.
type querySpec struct {
	expr   string
	window string
}

var queries = []querySpec{
	{expr: `{job="bench"}`, window: "5m"},
	{expr: `rate({job="bench"}[1m])`, window: "5m"},
	{expr: `count_over_time({job="bench"}[5m])`, window: "10m"},
}

type service struct {
	counter atomic.Uint64
}

func NewLokiLoadGeneratorService() load_generator_service.IStackLoadGenerator {
	return &service{}
}

func (s *service) GenerateQueries(ctx context.Context, port int) error {
	idx := s.counter.Add(1) - 1
	q := queries[idx%uint64(len(queries))]

	end := time.Now().UnixNano()
	windowDur, _ := time.ParseDuration(q.window)
	start := time.Now().Add(-windowDur).UnixNano()

	u := fmt.Sprintf(
		"http://localhost:%d/loki/api/v1/query_range?query=%s&limit=50&start=%d&end=%d",
		port,
		url.QueryEscape(q.expr),
		start,
		end,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build loki query: %w", err)
	}
	req.Close = true
	resp, err := portforwardhttp.Client.Do(req)
	if err != nil {
		return fmt.Errorf("loki query_range: %w", err)
	}
	defer portforwardhttp.CloseResp(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("loki returned status %d", resp.StatusCode)
	}
	return nil
}
