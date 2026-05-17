package config

import (
	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	DockerHubNamespace string
	StorageDSN         string
	SQLDebug           bool
	Topology           ClusterLayout
}

type envConfig struct {
	DockerHubNamespace string `env:"OBS_BENCH_DOCKERHUB_NAMESPACE"`
	StorageDSN         string `env:"OBS_BENCH_STORAGE_DSN"        env-default:"file:../data/obs_bench_results.db?cache=shared&mode=rwc"`
	SQLDebug           bool   `env:"OBS_BENCH_SQL_DEBUG"          env-default:"false"`

	// Имена helm-релизов / сервисов для построения defaultTopology
	PrometheusRelease string `env:"OBS_BENCH_PROMETHEUS_RELEASE"        env-default:"prometheus"`
	VMSingleService   string `env:"OBS_BENCH_VM_SINGLE_SERVICE"         env-default:"vmsingle-victoria-metrics-k8s-stack"`
	LokiService       string `env:"OBS_BENCH_LOKI_QUERY_SERVICE"        env-default:"loki"`
	OpenSearchService string `env:"OBS_BENCH_OPENSEARCH_QUERY_SERVICE"  env-default:"opensearch-cluster-master"`

	// Topology overrides (опциональные, перекрывают defaultTopology)
	MonitoringNamespace       string `env:"OBS_BENCH_MONITORING_NAMESPACE"`
	CentralPrometheusService  string `env:"OBS_BENCH_CENTRAL_PROMETHEUS_SERVICE"`
	MetricsProviderNamespace  string `env:"OBS_BENCH_METRICS_PROVIDER_NAMESPACE"`
	LogProducerNamespace      string `env:"OBS_BENCH_LOG_PRODUCER_NAMESPACE"`
	PrometheusTargetNamespace string `env:"OBS_BENCH_PROMETHEUS_TARGET_NAMESPACE"`
	VictoriaTargetNamespace   string `env:"OBS_BENCH_VICTORIA_TARGET_NAMESPACE"`
	LokiTargetNamespace       string `env:"OBS_BENCH_LOKI_TARGET_NAMESPACE"`
	OpenSearchTargetNamespace string `env:"OBS_BENCH_OPENSEARCH_TARGET_NAMESPACE"`
	PrometheusQueryLocalPort  int    `env:"OBS_BENCH_PROMETHEUS_QUERY_LOCAL_PORT"`
	VictoriaQueryLocalPort    int    `env:"OBS_BENCH_VICTORIA_QUERY_LOCAL_PORT"`
	LokiQueryLocalPort        int    `env:"OBS_BENCH_LOKI_QUERY_LOCAL_PORT"`
	OpenSearchQueryLocalPort  int    `env:"OBS_BENCH_OPENSEARCH_QUERY_LOCAL_PORT"`
}

func NewConfig() (*Config, error) {
	var e envConfig
	if err := cleanenv.ReadConfig(".env", &e); err != nil {
		if err := cleanenv.ReadEnv(&e); err != nil {
			return nil, err
		}
	}

	topo := defaultTopology(e.PrometheusRelease, e.VMSingleService, e.LokiService, e.OpenSearchService)
	applyOverrides(&topo, &e)
	if err := topo.ValidateInstrumentCoverage(); err != nil {
		return nil, err
	}

	return &Config{
		DockerHubNamespace: e.DockerHubNamespace,
		StorageDSN:         e.StorageDSN,
		SQLDebug:           e.SQLDebug,
		Topology:           topo,
	}, nil
}

func applyOverrides(t *ClusterLayout, e *envConfig) {
	if e.MonitoringNamespace != "" {
		t.CentralMonitoring.Namespace = e.MonitoringNamespace
	}
	if e.CentralPrometheusService != "" {
		t.CentralMonitoring.PrometheusServiceName = e.CentralPrometheusService
	}
	if e.MetricsProviderNamespace != "" {
		t.MetricsProviderNamespace = e.MetricsProviderNamespace
	}
	if e.LogProducerNamespace != "" {
		t.LogProducerNamespace = e.LogProducerNamespace
	}
	if e.PrometheusTargetNamespace != "" {
		t.Prometheus.DeployNamespace = e.PrometheusTargetNamespace
		t.Prometheus.PVCQueryNamespace = e.PrometheusTargetNamespace
	}
	if e.VictoriaTargetNamespace != "" {
		t.VictoriaMetrics.DeployNamespace = e.VictoriaTargetNamespace
		t.VictoriaMetrics.PVCQueryNamespace = e.VictoriaTargetNamespace
	}
	if e.LokiTargetNamespace != "" {
		t.Loki.DeployNamespace = e.LokiTargetNamespace
		t.Loki.PVCQueryNamespace = e.LokiTargetNamespace
	}
	if e.OpenSearchTargetNamespace != "" {
		t.OpenSearch.DeployNamespace = e.OpenSearchTargetNamespace
		t.OpenSearch.PVCQueryNamespace = e.OpenSearchTargetNamespace
	}
	if e.PrometheusQueryLocalPort != 0 {
		t.Prometheus.QueryLocalPort = e.PrometheusQueryLocalPort
	}
	if e.VictoriaQueryLocalPort != 0 {
		t.VictoriaMetrics.QueryLocalPort = e.VictoriaQueryLocalPort
	}
	if e.LokiQueryLocalPort != 0 {
		t.Loki.QueryLocalPort = e.LokiQueryLocalPort
	}
	if e.OpenSearchQueryLocalPort != 0 {
		t.OpenSearch.QueryLocalPort = e.OpenSearchQueryLocalPort
	}
}
