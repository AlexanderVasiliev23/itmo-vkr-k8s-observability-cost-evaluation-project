package victoria_metrics_resources_usage_info_collector_service

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"obs-bench/internal/config"
	"obs-bench/internal/models"
	"obs-bench/internal/pkg/portforwardhttp"
	"obs-bench/internal/pkg/promquery"
	"obs-bench/internal/pkg/resourcepromql"
	"obs-bench/internal/providers/kubernetes"
	resources_usage_info_collector_service "obs-bench/internal/services/resources-usage-info-collector"
)

var _ resources_usage_info_collector_service.IStackResourcesUsageInfoCollector = &service{}

type service struct {
	kubernetesProvider kubernetes.IKubernetesProvider
	central            config.CentralMonitoring
	target             config.InstrumentTarget
	httpClient         *http.Client
}

func NewVictoriaMetricsResourcesUsageInfoCollectorService(
	kubernetesProvider kubernetes.IKubernetesProvider,
	central config.CentralMonitoring,
	target config.InstrumentTarget,
) resources_usage_info_collector_service.IStackResourcesUsageInfoCollector {
	return &service{
		kubernetesProvider: kubernetesProvider,
		central:            central,
		target:             target,
		httpClient:         portforwardhttp.Client,
	}
}

func (s *service) Collect(ctx context.Context, duration time.Duration) (*models.ResourcesUsageInfoModel, error) {
	if s.target.CadvisorPodSelector == "" && s.target.CadvisorContainerName == "" {
		if err := promquery.ValidateJobLabel(s.target.ProcessMetricsJob); err != nil {
			return nil, err
		}
	}

	stopCh, err := s.kubernetesProvider.PortForwardService(
		ctx,
		s.central.Namespace,
		s.central.PrometheusServiceName,
		s.central.PrometheusLocalPort,
		s.central.PrometheusRemotePort,
	)
	if err != nil {
		return nil, err
	}
	defer close(stopCh)

	baseURL := fmt.Sprintf("http://localhost:%d", s.central.PrometheusLocalPort)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	durSec := int(duration.Seconds())
	cpuQuery, memAvgQuery, memPeakQuery, diskPromQL, err := resourcepromql.ResourceQueries(s.target, durSec)
	if err != nil {
		return nil, err
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}

		diskBytes, err := promquery.QueryInstantScalar(ctx, s.httpClient, baseURL, diskPromQL)
		if err != nil {
			slog.ErrorContext(ctx, "error querying disk metric", "query", diskPromQL, "err", err)
			continue
		}

		cpuCores, err := promquery.QueryInstantScalar(ctx, s.httpClient, baseURL, cpuQuery)
		if err != nil {
			slog.ErrorContext(ctx, "error querying cpu", "err", err)
			continue
		}

		memAvg, err := promquery.QueryInstantScalar(ctx, s.httpClient, baseURL, memAvgQuery)
		if err != nil {
			slog.ErrorContext(ctx, "error querying avg memory", "err", err)
			continue
		}

		memPeak, err := promquery.QueryInstantScalar(ctx, s.httpClient, baseURL, memPeakQuery)
		if err != nil {
			slog.ErrorContext(ctx, "error querying peak memory", "err", err)
			continue
		}

		return &models.ResourcesUsageInfoModel{
			CPUCores:     cpuCores,
			MemAvgBytes:  int64(memAvg),
			MemPeakBytes: int64(memPeak),
			DiskBytes:    int64(diskBytes),
		}, nil
	}
}
