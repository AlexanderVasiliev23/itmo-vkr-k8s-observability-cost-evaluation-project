package monitoring

import (
	"context"
	"obs-bench/internal/config"
	"obs-bench/internal/providers/helm"
	"obs-bench/internal/providers/kubernetes"
)

type IMonitoringService interface {
	UpMonitoring(ctx context.Context) error
}

type service struct {
	kubernetesProvider kubernetes.IKubernetesProvider
	helmProvider       helm.IHelmProvider
	cfg                *config.Config
}

func NewMonitoringService(
	kubernetesProvider kubernetes.IKubernetesProvider,
	helmProvider helm.IHelmProvider,
	cfg *config.Config,
) IMonitoringService {
	return &service{
		kubernetesProvider: kubernetesProvider,
		helmProvider:       helmProvider,
		cfg:                cfg,
	}
}

func (s *service) UpMonitoring(ctx context.Context) error {
	namespace := s.cfg.Topology.CentralMonitoring.Namespace
	releaseName := s.cfg.Topology.CentralMonitoring.StackHelmReleaseName

	if err := s.helmProvider.TryUninstall(ctx, releaseName); err != nil {
		return err
	}
	if err := s.kubernetesProvider.DeleteKubePrometheusStackWebhooks(ctx); err != nil {
		return err
	}
	if err := s.kubernetesProvider.DeleteNamespace(ctx, namespace); err != nil {
		return err
	}

	vals := map[string]interface{}{
		"grafana": map[string]interface{}{
			"enabled": false,
		},
		"alertmanager": map[string]interface{}{
			"enabled": false,
		},
		"prometheus-node-exporter": map[string]interface{}{
			"enabled": false,
		},
		"nodeExporter": map[string]interface{}{
			"enabled": false,
		},
		"kube-state-metrics": map[string]interface{}{
			"enabled": false,
		},
		"kubeEtcd": map[string]interface{}{
			"enabled": false,
		},
		"kubeControllerManager": map[string]interface{}{
			"enabled": false,
		},
		"kubeScheduler": map[string]interface{}{
			"enabled": false,
		},
		"kubeProxy": map[string]interface{}{
			"enabled": false,
		},
		"prometheusOperator": map[string]interface{}{
			"enabled": true,
		},
		"prometheus": map[string]interface{}{
			"enabled": true,
			"prometheusSpec": map[string]interface{}{
				"scrapeInterval": "15s",
				"resources": map[string]interface{}{
					"limits": map[string]interface{}{
						"memory": "4Gi",
						"cpu":    "2",
					},
					"requests": map[string]interface{}{
						"memory": "1Gi",
						"cpu":    "500m",
					},
				},
			},
		},
	}

	const (
		repoURL   = "https://prometheus-community.github.io/helm-charts"
		chartName = "kube-prometheus-stack"
	)

	if err := s.helmProvider.Up(ctx, namespace, vals, repoURL, chartName, releaseName); err != nil {
		return err
	}

	return nil
}
