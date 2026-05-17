package victoria_metrics_load_generator_service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"

	"obs-bench/internal/pkg/portforwardhttp"
	load_generator_service "obs-bench/internal/services/load-generator"
)

var _ load_generator_service.IStackLoadGenerator = &service{}

// Query API VictoriaMetrics совместим с Prometheus (/api/v1/query, PromQL).
// QPS=1 фиксирован как контрольная переменная — нагрузка на чтение постоянна во всех
// экспериментах, чтобы изолировать стоимость ingestion. Три типа запросов к реально
// экспортируемой метрике bench_series_value (gauge от metrics-provider).
var queries = []string{
	"sum(bench_series_value)",
	"avg(bench_series_value)",
	"max(bench_series_value)",
}

type service struct {
	counter atomic.Uint64
}

// NewVictoriaMetricsLoadGeneratorService создаёт генератор нагрузки к VictoriaMetrics single-node.
func NewVictoriaMetricsLoadGeneratorService() load_generator_service.IStackLoadGenerator {
	return &service{}
}

func (s *service) GenerateQueries(ctx context.Context, port int) error {
	idx := s.counter.Add(1) - 1
	query := queries[idx%uint64(len(queries))]

	queryURL := fmt.Sprintf("http://localhost:%d/api/v1/query", port)
	params := url.Values{}
	params.Set("query", query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL+"?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Close = true

	resp, err := portforwardhttp.Client.Do(req)
	if err != nil {
		return fmt.Errorf("victoria metrics query: %w", err)
	}
	defer portforwardhttp.CloseResp(resp)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("victoria metrics returned status %d", resp.StatusCode)
	}

	return nil
}
