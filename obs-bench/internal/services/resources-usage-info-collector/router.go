package resources_usage_info_collector_service

import (
	"context"
	"time"

	"obs-bench/internal/enum"
	instr "obs-bench/internal/instrument"
	"obs-bench/internal/models"
)

type instrumentRouter struct {
	byInstrument map[enum.Instrument]IStackResourcesUsageInfoCollector
}

func NewInstrumentRouterFromMap(byInstrument map[enum.Instrument]IStackResourcesUsageInfoCollector) (IResourcesUsageInfoCollector, error) {
	if err := enum.EnsureAllInstrumentsInMap(byInstrument); err != nil {
		return nil, err
	}
	return &instrumentRouter{byInstrument: byInstrument}, nil
}

func (r *instrumentRouter) Collect(ctx context.Context, inst enum.Instrument, duration time.Duration) (*models.ResourcesUsageInfoModel, error) {
	c, err := instr.Lookup(r.byInstrument, inst)
	if err != nil {
		return nil, err
	}
	return c.Collect(ctx, duration)
}
