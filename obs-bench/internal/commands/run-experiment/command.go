package run_experiment

import (
	"fmt"
	"obs-bench/internal/enum"
	experiment_usecase "obs-bench/internal/usecases/experiment"
	"time"

	"github.com/spf13/cobra"
)

type Args struct {
	instrument    string
	loadValue     int
	retentionDays int
	duration      time.Duration
}

func NewRunExperimentCommand(experimentUsecase experiment_usecase.IExperimentUsecase, myArgs *Args) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run-experiment",
		Short: "Запуск эксперимента",
		Args:  cobra.MaximumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			if myArgs.loadValue <= 0 {
				return fmt.Errorf("load-value must be a positive integer")
			}
			switch myArgs.retentionDays {
			case 1, 7, 30:
			default:
				return fmt.Errorf("retention-days must be one of: 1, 7, 30")
			}
			if myArgs.duration <= 0 {
				return fmt.Errorf("duration must be positive")
			}

			inst, err := enum.ParseInstrument(myArgs.instrument)
			if err != nil {
				return err
			}

			return experimentUsecase.RunExperiment(cmd.Context(), inst, myArgs.loadValue, myArgs.retentionDays, myArgs.duration)
		},
	}

	cmd.Flags().StringVarP(&myArgs.instrument, "instrument", "i", "", "Инструмент: "+enum.InstrumentFlagChoices())
	cmd.Flags().IntVarP(&myArgs.loadValue, "load-value", "s", 0, "Ось нагрузки: cardinality (метрики) / logs/sec (логи)")
	cmd.Flags().IntVar(&myArgs.loadValue, "series", 0, "Alias: load-value")
	cmd.Flags().IntVar(&myArgs.retentionDays, "retention-days", 7, "retention_days (policy): 1, 7, 30")
	cmd.Flags().DurationVarP(&myArgs.duration, "duration", "d", 0, "Время эксперимента (обязательный, например 3h)")
	_ = cmd.MarkFlagRequired("duration")
	_ = cmd.MarkFlagRequired("instrument")

	return cmd
}
