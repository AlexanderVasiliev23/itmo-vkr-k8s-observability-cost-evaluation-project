package commands

import (
	run_batch "obs-bench/internal/commands/run-batch"
	run_experiment "obs-bench/internal/commands/run-experiment"
	experiment_usecase "obs-bench/internal/usecases/experiment"

	"github.com/spf13/cobra"
)

func NewRootCommand(experimentUsecase experiment_usecase.IExperimentUsecase) *cobra.Command {
	rootCmd := &cobra.Command{
		Short: "ObsBench — утилита для проведения экспериментов по observability в Kubernetes",
		Long: `ObsBench развёртывает observability-стек, прогоняет нагрузку и сохраняет
замеры (RAM, CPU, диск) в SQLite. Анализ и оценки — в obs-advisor.`,
	}
	rootCmd.AddCommand(func() *cobra.Command {
		return run_experiment.NewRunExperimentCommand(experimentUsecase, &run_experiment.Args{})
	}())
	rootCmd.AddCommand(run_batch.NewRunBatchCommand(experimentUsecase))

	return rootCmd
}
