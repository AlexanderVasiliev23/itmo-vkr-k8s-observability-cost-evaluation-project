package experimentworkload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"obs-bench/internal/config"
	"obs-bench/internal/enum"
	"obs-bench/internal/providers/kubernetes"
	load_generator_service "obs-bench/internal/services/load-generator"
	log_provider "obs-bench/internal/services/log-provider"
	metrics_provider "obs-bench/internal/services/metrics-provider"
)

type Workload interface {
	Run(ctx context.Context, instrument enum.Instrument, series int, duration time.Duration) error
}

type workload struct {
	metricsProvider    metrics_provider.IMetricsProviderService
	logProvider        log_provider.ILogProviderService
	kubernetesProvider kubernetes.IKubernetesProvider
	loadGenerator      load_generator_service.ILoadGenerator
	cfg                *config.Config
}

func NewWorkload(
	metricsProvider metrics_provider.IMetricsProviderService,
	logProvider log_provider.ILogProviderService,
	kubernetesProvider kubernetes.IKubernetesProvider,
	loadGenerator load_generator_service.ILoadGenerator,
	cfg *config.Config,
) Workload {
	return &workload{
		metricsProvider:    metricsProvider,
		logProvider:        logProvider,
		kubernetesProvider: kubernetesProvider,
		loadGenerator:      loadGenerator,
		cfg:                cfg,
	}
}

func (w *workload) Run(ctx context.Context, instrument enum.Instrument, series int, duration time.Duration) error {
	if enum.IsLogBackend(instrument) {
		return w.runLogExperiment(ctx, instrument, series, duration)
	}
	if err := w.runMetricIngestionLoad(ctx, series); err != nil {
		return err
	}
	return w.runQueryLoad(ctx, instrument, duration)
}

func (w *workload) runMetricIngestionLoad(ctx context.Context, series int) error {
	return w.metricsProvider.UpMetricsProvider(ctx, w.cfg.Topology.MetricsProviderNamespace, series)
}

func (w *workload) runLogExperiment(ctx context.Context, instrument enum.Instrument, logsPerSec int, duration time.Duration) error {
	if err := w.logProvider.UpLogProvider(ctx, instrument, logsPerSec); err != nil {
		return err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := w.kubernetesProvider.DeleteLogProducerDeployment(stopCtx, w.cfg.Topology.LogProducerNamespace); err != nil {
			slog.Warn("delete log-producer", "err", err)
		}
	}()

	return w.runQueryLoad(ctx, instrument, duration)
}

func (w *workload) runQueryLoad(ctx context.Context, instrument enum.Instrument, duration time.Duration) error {
	target, err := w.cfg.Topology.InstrumentTarget(instrument)
	if err != nil {
		return err
	}

	stopCh, err := w.kubernetesProvider.PortForwardService(
		ctx,
		target.DeployNamespace,
		target.QueryServiceName,
		target.QueryLocalPort,
		target.QueryRemotePort,
	)
	if err != nil {
		return err
	}
	defer close(stopCh)

	if err := w.waitForQueryAPI(ctx, instrument, target.QueryLocalPort, 90*time.Second); err != nil {
		return err
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

outer:
	for range ticker.C {
		select {
		case <-timer.C:
			break outer
		default:
		}

		if err := w.loadGenerator.GenerateQueries(ctx, instrument, target.QueryLocalPort); err != nil {
			if transientQueryErr(err) {
				slog.WarnContext(ctx, "transient query error, skipping tick", "err", err)
				continue
			}
			return err
		}
	}

	return nil
}

func transientQueryErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "EOF") ||
		strings.Contains(s, "status 404")
}

func (w *workload) waitForQueryAPI(ctx context.Context, instrument enum.Instrument, localPort int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	backoff := 500 * time.Millisecond
	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := w.loadGenerator.GenerateQueries(ctx, instrument, localPort)
		if err == nil {
			return nil
		}
		lastErr = err
		if !transientQueryErr(err) {
			return err
		}
		slog.WarnContext(ctx, "query API not ready, retry", "port", localPort, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 2*time.Second {
			backoff += 250 * time.Millisecond
		}
	}
	if lastErr != nil {
		return fmt.Errorf("query API not ready after %v: %w", timeout, lastErr)
	}
	return fmt.Errorf("query API not ready after %v", timeout)
}
