package run_batch

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"obs-bench/internal/enum"
	experiment_usecase "obs-bench/internal/usecases/experiment"
)

type batchFile struct {
	Experiments []experimentEntry `yaml:"experiments"`
}

type experimentEntry struct {
	Instrument    string        `yaml:"instrument"`
	LoadValue     int           `yaml:"load_value"`
	RetentionDays int           `yaml:"retention_days"`
	Duration      time.Duration `yaml:"duration"`
}

func (e *experimentEntry) validate() error {
	if e.LoadValue <= 0 {
		return fmt.Errorf("load_value must be a positive integer")
	}
	switch e.RetentionDays {
	case 1, 7, 30:
	default:
		return fmt.Errorf("retention_days must be one of: 1, 7, 30")
	}
	if e.Duration <= 0 {
		return fmt.Errorf("duration must be positive")
	}
	if _, err := enum.ParseInstrument(e.Instrument); err != nil {
		return err
	}
	return nil
}

func NewRunBatchCommand(experimentUsecase experiment_usecase.IExperimentUsecase) *cobra.Command {
	var filePath string

	cmd := &cobra.Command{
		Use:   "run-batch",
		Short: "Последовательный запуск нескольких экспериментов из YAML-файла",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("read batch file: %w", err)
			}

			var batch batchFile
			if err := yaml.Unmarshal(data, &batch); err != nil {
				return fmt.Errorf("parse batch file: %w", err)
			}

			if len(batch.Experiments) == 0 {
				return fmt.Errorf("no experiments defined in %s", filePath)
			}

			succeeded, failed := 0, 0
			for i, e := range batch.Experiments {
				slog.Info("starting experiment", "index", i+1, "total", len(batch.Experiments),
					"instrument", e.Instrument, "load_value", e.LoadValue,
					"retention_days", e.RetentionDays, "duration", e.Duration)

				if err := e.validate(); err != nil {
					slog.Error("experiment skipped: invalid config", "index", i+1, "err", err)
					failed++
					continue
				}

				inst, _ := enum.ParseInstrument(e.Instrument)
				if err := experimentUsecase.RunExperiment(cmd.Context(), inst, e.LoadValue, e.RetentionDays, e.Duration); err != nil {
					slog.Error("experiment failed", "index", i+1, "instrument", e.Instrument,
						"load_value", e.LoadValue, "err", err)
					failed++
					continue
				}

				slog.Info("experiment done", "index", i+1, "instrument", e.Instrument, "load_value", e.LoadValue)
				succeeded++
			}

			slog.Info("batch complete", "succeeded", succeeded, "failed", failed, "total", len(batch.Experiments))
			if failed > 0 {
				return fmt.Errorf("%d experiment(s) failed", failed)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&filePath, "file", "f", "", "Путь к YAML-файлу со списком экспериментов")
	_ = cmd.MarkFlagRequired("file")

	return cmd
}
