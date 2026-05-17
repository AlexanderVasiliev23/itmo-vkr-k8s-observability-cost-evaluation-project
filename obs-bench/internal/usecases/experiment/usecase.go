package experiment_usecase

import (
	"context"
	"log/slog"

	"obs-bench/internal/enum"
	resources_usage_info_storage_provider "obs-bench/internal/providers/resources-usage-info-storage"
	"obs-bench/internal/services/experimentstack"
	"obs-bench/internal/services/experimentworkload"
	"obs-bench/internal/services/monitoring"
	resources_usage_info_collector_service "obs-bench/internal/services/resources-usage-info-collector"
	"time"
)

const warmupDuration = 15 * time.Minute

type IExperimentUsecase interface {
	RunExperiment(ctx context.Context, instrument enum.Instrument, loadValue int, retentionDays int, duration time.Duration) error
}

type usecase struct {
	monitoringService                 monitoring.IMonitoringService
	instrumentStack                   experimentstack.InstrumentDeployer
	instrumentWorkload                experimentworkload.Workload
	resourcesCollector                resources_usage_info_collector_service.IResourcesUsageInfoCollector
	resourcesUsageInfoStorageProvider resources_usage_info_storage_provider.IResourcesUsageInfoStorageProvider
}

func NewExperimentUsecase(
	monitoringService monitoring.IMonitoringService,
	instrumentStack experimentstack.InstrumentDeployer,
	instrumentWorkload experimentworkload.Workload,
	resourcesCollector resources_usage_info_collector_service.IResourcesUsageInfoCollector,
	resourcesUsageInfoStorageProvider resources_usage_info_storage_provider.IResourcesUsageInfoStorageProvider,
) IExperimentUsecase {
	return &usecase{
		monitoringService:                 monitoringService,
		instrumentStack:                   instrumentStack,
		instrumentWorkload:                instrumentWorkload,
		resourcesCollector:                resourcesCollector,
		resourcesUsageInfoStorageProvider: resourcesUsageInfoStorageProvider,
	}
}

func (u *usecase) RunExperiment(ctx context.Context, instrument enum.Instrument, loadValue int, retentionDays int, duration time.Duration) error {
	collectWindow := duration - warmupDuration
	if collectWindow <= 0 {
		slog.Warn("duration shorter than warmup, using full duration as collect window (smoke mode)",
			"duration", duration, "warmup", warmupDuration)
		collectWindow = duration
	}

	if err := u.monitoringService.UpMonitoring(ctx); err != nil {
		return err
	}

	if err := u.instrumentStack.Deploy(ctx, instrument, retentionDays); err != nil {
		return err
	}

	if err := u.instrumentWorkload.Run(ctx, instrument, loadValue, duration); err != nil {
		return err
	}

	resourcesUsageInfo, err := u.resourcesCollector.Collect(ctx, instrument, collectWindow)
	if err != nil {
		return err
	}

	if enum.IsLogBackend(instrument) {
		resourcesUsageInfo.WorkloadType = "logs"
	} else {
		resourcesUsageInfo.WorkloadType = "metrics"
	}
	resourcesUsageInfo.LoadValue = loadValue
	resourcesUsageInfo.RetentionDays = retentionDays
	resourcesUsageInfo.DurationSeconds = int(duration.Seconds())
	resourcesUsageInfo.Instrument = string(instrument)

	if err := u.resourcesUsageInfoStorageProvider.Save(ctx, resourcesUsageInfo); err != nil {
		return err
	}

	return nil
}
