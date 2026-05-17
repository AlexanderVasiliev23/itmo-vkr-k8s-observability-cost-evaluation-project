package resources_usage_info_collector_service

import (
	"context"
	"obs-bench/internal/enum"
	"obs-bench/internal/models"
	"time"
)

type IStackResourcesUsageInfoCollector interface {
	Collect(ctx context.Context, duration time.Duration) (*models.ResourcesUsageInfoModel, error)
}

type IResourcesUsageInfoCollector interface {
	Collect(ctx context.Context, instrument enum.Instrument, duration time.Duration) (*models.ResourcesUsageInfoModel, error)
}
