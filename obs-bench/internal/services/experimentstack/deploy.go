package experimentstack

import (
	"context"

	"obs-bench/internal/config"
	"obs-bench/internal/enum"
	instr "obs-bench/internal/instrument"
	"obs-bench/internal/services/loki"
	"obs-bench/internal/services/opensearch"
	"obs-bench/internal/services/prometheus"
	victoria_metrics "obs-bench/internal/services/victoria-metrics"
)

type Stack interface {
	Deploy(ctx context.Context, retentionDays int) error
}

type InstrumentDeployer interface {
	Deploy(ctx context.Context, instrument enum.Instrument, retentionDays int) error
}

type instrumentRouter struct {
	byInstrument map[enum.Instrument]Stack
}

func NewInstrumentDeployerFromMap(byInstrument map[enum.Instrument]Stack) (InstrumentDeployer, error) {
	if err := enum.EnsureAllInstrumentsInMap(byInstrument); err != nil {
		return nil, err
	}
	return &instrumentRouter{byInstrument: byInstrument}, nil
}

func (r *instrumentRouter) Deploy(ctx context.Context, inst enum.Instrument, retentionDays int) error {
	s, err := instr.Lookup(r.byInstrument, inst)
	if err != nil {
		return err
	}
	return s.Deploy(ctx, retentionDays)
}

type prometheusStack struct {
	svc    prometheus.IPrometheusService
	target config.InstrumentTarget
}

func NewPrometheusStack(svc prometheus.IPrometheusService, target config.InstrumentTarget) Stack {
	return &prometheusStack{svc: svc, target: target}
}

func (s *prometheusStack) Deploy(ctx context.Context, retentionDays int) error {
	return s.svc.UpPrometheusStack(ctx, s.target.DeployNamespace, retentionDays)
}

type victoriaStack struct {
	svc    victoria_metrics.IVictoriaMetricsService
	target config.InstrumentTarget
}

func NewVictoriaStack(svc victoria_metrics.IVictoriaMetricsService, target config.InstrumentTarget) Stack {
	return &victoriaStack{svc: svc, target: target}
}

func (s *victoriaStack) Deploy(ctx context.Context, retentionDays int) error {
	return s.svc.UpVictoriaMetricsStack(ctx, s.target.DeployNamespace, retentionDays)
}

type lokiStack struct {
	svc    loki.ILokiService
	target config.InstrumentTarget
}

func NewLokiStack(svc loki.ILokiService, target config.InstrumentTarget) Stack {
	return &lokiStack{svc: svc, target: target}
}

func (s *lokiStack) Deploy(ctx context.Context, retentionDays int) error {
	return s.svc.UpLokiStack(ctx, s.target.DeployNamespace, retentionDays)
}

type openSearchStack struct {
	svc    opensearch.IOpenSearchService
	target config.InstrumentTarget
}

func NewOpenSearchStack(svc opensearch.IOpenSearchService, target config.InstrumentTarget) Stack {
	return &openSearchStack{svc: svc, target: target}
}

func (s *openSearchStack) Deploy(ctx context.Context, retentionDays int) error {
	return s.svc.UpOpenSearchStack(ctx, s.target.DeployNamespace, retentionDays)
}
