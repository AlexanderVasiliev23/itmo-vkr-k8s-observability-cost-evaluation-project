package opensearch_load_generator_service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"obs-bench/internal/pkg/portforwardhttp"
	load_generator_service "obs-bench/internal/services/load-generator"
)

var _ load_generator_service.IStackLoadGenerator = &service{}

// Три типа запросов имитируют типичную нагрузку Grafana-дашборда над OpenSearch:
// полный скан, term-фильтр и агрегацию. QPS=1 фиксирован как контрольная переменная —
// нагрузка на чтение постоянна во всех экспериментах, чтобы изолировать стоимость ingestion.
var queryBodies = []string{
	// полный скан — базовый запрос, эквивалент "show all logs"
	`{"size":10,"query":{"match_all":{}}}`,
	// match-фильтр по полю msg + сортировка по ts — имитирует Grafana-фильтр по тексту
	`{"size":10,"query":{"match":{"msg":"bench"}},"sort":[{"ts":{"order":"desc"}}]}`,
	// агрегация value_count по полю i — имитирует panel с подсчётом событий
	`{"size":0,"aggs":{"event_count":{"value_count":{"field":"i"}}}}`,
}

type service struct {
	counter atomic.Uint64
}

func NewOpenSearchLoadGeneratorService() load_generator_service.IStackLoadGenerator {
	return &service{}
}

func (s *service) GenerateQueries(ctx context.Context, port int) error {
	idx := s.counter.Add(1) - 1
	body := queryBodies[idx%uint64(len(queryBodies))]

	u := fmt.Sprintf("http://localhost:%d/logbench/_search", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("build opensearch search: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Close = true
	resp, err := portforwardhttp.Client.Do(req)
	if err != nil {
		return fmt.Errorf("opensearch search: %w", err)
	}
	defer portforwardhttp.CloseResp(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("opensearch returned status %d", resp.StatusCode)
	}
	return nil
}
