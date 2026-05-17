package victoria_metrics

import (
	"context"
	"fmt"

	"obs-bench/internal/config"
	"obs-bench/internal/pkg/diskexporter"
	"obs-bench/internal/providers/docker"
	docker_registry "obs-bench/internal/providers/docker-registry"
	"obs-bench/internal/providers/helm"
	"obs-bench/internal/providers/kubernetes"
)

type IVictoriaMetricsService interface {
	UpVictoriaMetricsStack(ctx context.Context, namespace string, retentionDays int) error
}

type service struct {
	kubernetesProvider       kubernetes.IKubernetesProvider
	helmProvider             helm.IHelmProvider
	dockerProvider           docker.IDockerProvider
	dockerRegistryProvider   docker_registry.IDockerRegistryProvider
	metricsProviderNamespace string
	dockerHubNamespace       string
}

func NewVictoriaMetricsService(
	kubernetesProvider kubernetes.IKubernetesProvider,
	helmProvider helm.IHelmProvider,
	dockerProvider docker.IDockerProvider,
	dockerRegistryProvider docker_registry.IDockerRegistryProvider,
	cfg *config.Config,
) IVictoriaMetricsService {
	return &service{
		kubernetesProvider:       kubernetesProvider,
		helmProvider:             helmProvider,
		dockerProvider:           dockerProvider,
		dockerRegistryProvider:   dockerRegistryProvider,
		metricsProviderNamespace: cfg.Topology.MetricsProviderNamespace,
		dockerHubNamespace:       cfg.DockerHubNamespace,
	}
}

func (s *service) UpVictoriaMetricsStack(ctx context.Context, namespace string, retentionDays int) error {
	tag, err := diskexporter.BuildDevImageTag(s.dockerHubNamespace)
	if err != nil {
		return err
	}

	if err := s.dockerProvider.RecreateImageWithNewTag(ctx, tag, diskexporter.ContextPath); err != nil {
		return err
	}

	if err := s.dockerRegistryProvider.PushImage(ctx, tag); err != nil {
		return err
	}

	if err := s.kubernetesProvider.DeleteVMWebhooks(ctx); err != nil {
		return err
	}

	const (
		repoURL     = "https://victoriametrics.github.io/helm-charts"
		chartName   = "victoria-metrics-k8s-stack"
		releaseName = "victoria-metrics-k8s-stack"
	)

	if err := s.helmProvider.TryUninstall(ctx, releaseName); err != nil {
		return err
	}

	if err := s.kubernetesProvider.DeleteVMClusterRoles(ctx); err != nil {
		return err
	}

	if err := s.kubernetesProvider.DeleteVMServiceScrape(ctx, namespace); err != nil {
		return err
	}

	if err := s.kubernetesProvider.DeleteNamespace(ctx, namespace); err != nil {
		return err
	}

	vals := map[string]interface{}{
		"grafana": map[string]interface{}{
			"enabled": false,
		},
		"prometheus-node-exporter": map[string]interface{}{
			"enabled": false,
		},
		"kubeScheduler": map[string]interface{}{
			"enabled": false,
		},
		"kubeControllerManager": map[string]interface{}{
			"enabled": false,
		},
		"kubeEtcd": map[string]interface{}{
			"enabled": false,
		},
		"vmagent": map[string]interface{}{
			"spec": map[string]interface{}{
				"extraArgs": map[string]interface{}{
					"enableTCP6":               "true",
					"promscrape.maxScrapeSize": "134217728",
				},
			},
		},
		"vmsingle": map[string]interface{}{
			"spec": map[string]interface{}{
				"retentionPeriod": fmt.Sprintf("%dd", retentionDays),
			},
		},
	}

	if err := s.helmProvider.Up(ctx, namespace, vals, repoURL, chartName, releaseName); err != nil {
		return err
	}

	if err := s.kubernetesProvider.DeleteVMWebhooks(ctx); err != nil {
		return err
	}

	if err := s.kubernetesProvider.CreateVMServiceScrape(ctx, namespace, s.metricsProviderNamespace); err != nil {
		return err
	}

	if err := s.kubernetesProvider.CreateDiskMetricsExporter(
		ctx,
		namespace,
		tag,
		"vmsingle-victoria-metrics-k8s-stack",
	); err != nil {
		return err
	}

	if err := s.kubernetesProvider.CreateDiskMetricsService(ctx, namespace); err != nil {
		return err
	}

	if err := s.kubernetesProvider.CreateServiceMonitor(ctx, namespace, "disk-metrics-exporter", "metrics", map[string]string{"app": "disk-metrics-exporter"}); err != nil {
		return err
	}

	if err := s.kubernetesProvider.CreateServiceMonitor(ctx, namespace, "vmsingle-victoria-metrics-k8s-stack", "http", map[string]string{
		"app.kubernetes.io/name":     "vmsingle",
		"app.kubernetes.io/instance": "victoria-metrics-k8s-stack",
	}); err != nil {
		return err
	}

	return nil
}
