package load_generator_service

import (
	"context"

	"obs-bench/internal/enum"
)

type IStackLoadGenerator interface {
	GenerateQueries(ctx context.Context, port int) error
}

type ILoadGenerator interface {
	GenerateQueries(ctx context.Context, instrument enum.Instrument, port int) error
}
