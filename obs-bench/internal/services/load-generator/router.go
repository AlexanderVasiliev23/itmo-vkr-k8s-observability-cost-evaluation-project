package load_generator_service

import (
	"context"

	"obs-bench/internal/enum"
	instr "obs-bench/internal/instrument"
)

type instrumentRouter struct {
	byInstrument map[enum.Instrument]IStackLoadGenerator
}

func NewInstrumentRouterFromMap(byInstrument map[enum.Instrument]IStackLoadGenerator) (ILoadGenerator, error) {
	if err := enum.EnsureAllInstrumentsInMap(byInstrument); err != nil {
		return nil, err
	}
	return &instrumentRouter{byInstrument: byInstrument}, nil
}

func (r *instrumentRouter) GenerateQueries(ctx context.Context, inst enum.Instrument, port int) error {
	g, err := instr.Lookup(r.byInstrument, inst)
	if err != nil {
		return err
	}
	return g.GenerateQueries(ctx, port)
}
