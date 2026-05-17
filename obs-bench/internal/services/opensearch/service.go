package opensearch

import (
	"context"

	"obs-bench/internal/config"
	"obs-bench/internal/pkg/diskexporter"
	"obs-bench/internal/providers/docker"
	docker_registry "obs-bench/internal/providers/docker-registry"
	"obs-bench/internal/providers/helm"
	"obs-bench/internal/providers/kubernetes"
)

type IOpenSearchService interface {
	UpOpenSearchStack(ctx context.Context, namespace string, retentionDays int) error
}

type service struct {
	kubernetesProvider     kubernetes.IKubernetesProvider
	helmProvider           helm.IHelmProvider
	dockerProvider         docker.IDockerProvider
	dockerRegistryProvider docker_registry.IDockerRegistryProvider
	dockerHubNamespace     string
}

func NewOpenSearchService(
	kubernetesProvider kubernetes.IKubernetesProvider,
	helmProvider helm.IHelmProvider,
	dockerProvider docker.IDockerProvider,
	dockerRegistryProvider docker_registry.IDockerRegistryProvider,
	cfg *config.Config,
) IOpenSearchService {
	return &service{
		kubernetesProvider:     kubernetesProvider,
		helmProvider:           helmProvider,
		dockerProvider:         dockerProvider,
		dockerRegistryProvider: dockerRegistryProvider,
		dockerHubNamespace:     cfg.DockerHubNamespace,
	}
}

func (s *service) UpOpenSearchStack(ctx context.Context, namespace string, _ int) error {
	const (
		repoURL   = "https://opensearch-project.github.io/helm-charts"
		chartName = "opensearch"
	)
	releaseName := "opensearch"

	if err := s.helmProvider.TryUninstall(ctx, releaseName); err != nil {
		return err
	}
	if err := s.kubernetesProvider.DeleteNamespace(ctx, namespace); err != nil {
		return err
	}

	const (
		clusterName = "opensearch-cluster"
		nodeGroup   = "master"
	)
	stsDataStem := clusterName + "-" + nodeGroup
	dataPVCName := stsDataStem + "-" + stsDataStem + "-0"

	vals := map[string]interface{}{
		"clusterName": clusterName,
		"nodeGroup":   nodeGroup,
		"singleNode":  true,
		"replicas":    1,
		"persistence": map[string]interface{}{
			"enabled": true,
			"size":    "20Gi",
		},
		"opensearchJavaOpts": "-Xms512m -Xmx512m",
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
		"config": map[string]interface{}{
			"opensearch.yml": "plugins.security.disabled: true\n",
		},
	}

	if err := s.helmProvider.Up(ctx, namespace, vals, repoURL, chartName, releaseName); err != nil {
		return err
	}

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
	if err := s.kubernetesProvider.CreateDiskMetricsExporter(ctx, namespace, tag, dataPVCName); err != nil {
		return err
	}
	if err := s.kubernetesProvider.CreateDiskMetricsService(ctx, namespace); err != nil {
		return err
	}
	return s.kubernetesProvider.CreateServiceMonitor(ctx, namespace, "disk-metrics-exporter", "metrics",
		map[string]string{"app": "disk-metrics-exporter"})
}
