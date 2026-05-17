package app

import (
	"context"

	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"obs-bench/internal/commands"
	"obs-bench/internal/config"
	"obs-bench/internal/enum"
	"obs-bench/internal/providers/docker"
	docker_registry "obs-bench/internal/providers/docker-registry"
	"obs-bench/internal/providers/helm"
	"obs-bench/internal/providers/kubernetes"
	resources_usage_info_storage_provider "obs-bench/internal/providers/resources-usage-info-storage"
	"obs-bench/internal/services/experimentstack"
	"obs-bench/internal/services/experimentworkload"
	load_generator_service "obs-bench/internal/services/load-generator"
	loki_load_generator_service "obs-bench/internal/services/load-generator/loki"
	opensearch_load_generator_service "obs-bench/internal/services/load-generator/opensearch"
	prometheus_load_generator_service "obs-bench/internal/services/load-generator/prometheus"
	victoria_metrics_load_generator_service "obs-bench/internal/services/load-generator/victoria-metrics"
	log_provider "obs-bench/internal/services/log-provider"
	"obs-bench/internal/services/loki"
	metrics_provider "obs-bench/internal/services/metrics-provider"
	"obs-bench/internal/services/monitoring"
	"obs-bench/internal/services/opensearch"
	"obs-bench/internal/services/prometheus"
	resources_usage_info_collector_service "obs-bench/internal/services/resources-usage-info-collector"
	prometheus_resources_usage_info_collector_service "obs-bench/internal/services/resources-usage-info-collector/prometheus"
	victoria_metrics_resources_usage_info_collector_service "obs-bench/internal/services/resources-usage-info-collector/victoria-metrics"
	victoria_metrics "obs-bench/internal/services/victoria-metrics"
	experiment_usecase "obs-bench/internal/usecases/experiment"
)

const (
	namePrometheusResourcesCollector = `name:"prometheus_resources_collector"`
	nameVictoriaResourcesCollector   = `name:"victoria_resources_collector"`
	nameLokiResourcesCollector       = `name:"loki_resources_collector"`
	nameOpenSearchResourcesCollector = `name:"opensearch_resources_collector"`
	namePrometheusLoadGenerator      = `name:"prometheus_load_generator"`
	nameVictoriaLoadGenerator        = `name:"victoria_load_generator"`
	nameLokiLoadGenerator            = `name:"loki_load_generator"`
	nameOpenSearchLoadGenerator      = `name:"opensearch_load_generator"`
	namePrometheusExperimentStack    = `name:"prometheus_experiment_stack"`
	nameVictoriaExperimentStack      = `name:"victoria_experiment_stack"`
	nameLokiExperimentStack          = `name:"loki_experiment_stack"`
	nameOpenSearchExperimentStack    = `name:"opensearch_experiment_stack"`
)

type stackResourcesCollectorsIn struct {
	fx.In

	Prometheus resources_usage_info_collector_service.IStackResourcesUsageInfoCollector `name:"prometheus_resources_collector"`
	Victoria   resources_usage_info_collector_service.IStackResourcesUsageInfoCollector `name:"victoria_resources_collector"`
	Loki       resources_usage_info_collector_service.IStackResourcesUsageInfoCollector `name:"loki_resources_collector"`
	OpenSearch resources_usage_info_collector_service.IStackResourcesUsageInfoCollector `name:"opensearch_resources_collector"`
}

type stackLoadGeneratorsIn struct {
	fx.In

	Prometheus load_generator_service.IStackLoadGenerator `name:"prometheus_load_generator"`
	Victoria   load_generator_service.IStackLoadGenerator `name:"victoria_load_generator"`
	Loki       load_generator_service.IStackLoadGenerator `name:"loki_load_generator"`
	OpenSearch load_generator_service.IStackLoadGenerator `name:"opensearch_load_generator"`
}

type experimentStacksIn struct {
	fx.In

	Prometheus experimentstack.Stack `name:"prometheus_experiment_stack"`
	Victoria   experimentstack.Stack `name:"victoria_experiment_stack"`
	Loki       experimentstack.Stack `name:"loki_experiment_stack"`
	OpenSearch experimentstack.Stack `name:"opensearch_experiment_stack"`
}

func instrumentMap4[T any](prom, vm, lk, os T) map[enum.Instrument]T {
	return map[enum.Instrument]T{
		enum.InstrumentPrometheus:      prom,
		enum.InstrumentVictoriaMetrics: vm,
		enum.InstrumentLoki:            lk,
		enum.InstrumentOpenSearch:      os,
	}
}

func Module(runCtx context.Context) fx.Option {
	return fx.Options(
		fx.Provide(
			config.NewConfig,
			docker.NewDockerProvider,
			docker_registry.New,
			kubernetes.NewKubernetesProvider,
			helm.NewHelmProvider,
			func(cfg *config.Config) (resources_usage_info_storage_provider.IResourcesUsageInfoStorageProvider, error) {
				return resources_usage_info_storage_provider.NewResourcesUsageInfoStorageProvider(
					context.Background(), cfg.StorageDSN, cfg.SQLDebug)
			},
		),
		fx.Provide(
			metrics_provider.NewMetricsProviderService,
			log_provider.NewLogProviderService,
			monitoring.NewMonitoringService,
			prometheus.NewPrometheusService,
			victoria_metrics.NewVictoriaMetricsService,
			loki.NewLokiService,
			opensearch.NewOpenSearchService,
			fx.Annotate(
				func(p prometheus.IPrometheusService, cfg *config.Config) experimentstack.Stack {
					return experimentstack.NewPrometheusStack(p, cfg.Topology.Prometheus)
				},
				fx.ResultTags(namePrometheusExperimentStack),
			),
			fx.Annotate(
				func(v victoria_metrics.IVictoriaMetricsService, cfg *config.Config) experimentstack.Stack {
					return experimentstack.NewVictoriaStack(v, cfg.Topology.VictoriaMetrics)
				},
				fx.ResultTags(nameVictoriaExperimentStack),
			),
			fx.Annotate(
				func(svc loki.ILokiService, cfg *config.Config) experimentstack.Stack {
					return experimentstack.NewLokiStack(svc, cfg.Topology.Loki)
				},
				fx.ResultTags(nameLokiExperimentStack),
			),
			fx.Annotate(
				func(svc opensearch.IOpenSearchService, cfg *config.Config) experimentstack.Stack {
					return experimentstack.NewOpenSearchStack(svc, cfg.Topology.OpenSearch)
				},
				fx.ResultTags(nameOpenSearchExperimentStack),
			),
			func(s experimentStacksIn) (experimentstack.InstrumentDeployer, error) {
				return experimentstack.NewInstrumentDeployerFromMap(instrumentMap4(
					s.Prometheus, s.Victoria, s.Loki, s.OpenSearch,
				))
			},
			fx.Annotate(
				prometheus_load_generator_service.NewPrometheusLoadGeneratorService,
				fx.ResultTags(namePrometheusLoadGenerator),
			),
			fx.Annotate(
				victoria_metrics_load_generator_service.NewVictoriaMetricsLoadGeneratorService,
				fx.ResultTags(nameVictoriaLoadGenerator),
			),
			fx.Annotate(
				loki_load_generator_service.NewLokiLoadGeneratorService,
				fx.ResultTags(nameLokiLoadGenerator),
			),
			fx.Annotate(
				opensearch_load_generator_service.NewOpenSearchLoadGeneratorService,
				fx.ResultTags(nameOpenSearchLoadGenerator),
			),
			func(g stackLoadGeneratorsIn) (load_generator_service.ILoadGenerator, error) {
				return load_generator_service.NewInstrumentRouterFromMap(instrumentMap4(
					g.Prometheus, g.Victoria, g.Loki, g.OpenSearch,
				))
			},
			fx.Annotate(
				func(k8s kubernetes.IKubernetesProvider, cfg *config.Config) resources_usage_info_collector_service.IStackResourcesUsageInfoCollector {
					return prometheus_resources_usage_info_collector_service.NewPrometheusResourcesUsageInfoCollectorService(
						k8s, cfg.Topology.CentralMonitoring, cfg.Topology.Prometheus)
				},
				fx.ResultTags(namePrometheusResourcesCollector),
			),
			fx.Annotate(
				func(k8s kubernetes.IKubernetesProvider, cfg *config.Config) resources_usage_info_collector_service.IStackResourcesUsageInfoCollector {
					return victoria_metrics_resources_usage_info_collector_service.NewVictoriaMetricsResourcesUsageInfoCollectorService(
						k8s, cfg.Topology.CentralMonitoring, cfg.Topology.VictoriaMetrics)
				},
				fx.ResultTags(nameVictoriaResourcesCollector),
			),
			fx.Annotate(
				func(k8s kubernetes.IKubernetesProvider, cfg *config.Config) resources_usage_info_collector_service.IStackResourcesUsageInfoCollector {
					return prometheus_resources_usage_info_collector_service.NewPrometheusResourcesUsageInfoCollectorService(
						k8s, cfg.Topology.CentralMonitoring, cfg.Topology.Loki)
				},
				fx.ResultTags(nameLokiResourcesCollector),
			),
			fx.Annotate(
				func(k8s kubernetes.IKubernetesProvider, cfg *config.Config) resources_usage_info_collector_service.IStackResourcesUsageInfoCollector {
					return prometheus_resources_usage_info_collector_service.NewPrometheusResourcesUsageInfoCollectorService(
						k8s, cfg.Topology.CentralMonitoring, cfg.Topology.OpenSearch)
				},
				fx.ResultTags(nameOpenSearchResourcesCollector),
			),
			func(c stackResourcesCollectorsIn) (resources_usage_info_collector_service.IResourcesUsageInfoCollector, error) {
				return resources_usage_info_collector_service.NewInstrumentRouterFromMap(instrumentMap4(
					c.Prometheus, c.Victoria, c.Loki, c.OpenSearch,
				))
			},
			experimentworkload.NewWorkload,
			experiment_usecase.NewExperimentUsecase,
			commands.NewRootCommand,
		),
		fx.Invoke(func(lc fx.Lifecycle, p resources_usage_info_storage_provider.IResourcesUsageInfoStorageProvider) {
			lc.Append(fx.Hook{
				OnStop: func(ctx context.Context) error {
					return p.Close()
				},
			})
		}),
		fx.Invoke(func(cmd *cobra.Command) error {
			cmd.SetContext(runCtx)
			return cmd.Execute()
		}),
	)
}
