package metrics_provider

import (
	"context"

	"obs-bench/internal/config"
	"obs-bench/internal/pkg/imageutil"
	"obs-bench/internal/providers/docker"
	docker_registry "obs-bench/internal/providers/docker-registry"
	"obs-bench/internal/providers/kubernetes"
)

type IMetricsProviderService interface {
	UpMetricsProvider(ctx context.Context, namespace string, series int) error
}

type service struct {
	dockerProvider         docker.IDockerProvider
	dockerRegistryProvider docker_registry.IDockerRegistryProvider
	kubernetesProvider     kubernetes.IKubernetesProvider
	dockerHubNamespace     string
}

func NewMetricsProviderService(
	dockerProvider docker.IDockerProvider,
	dockerRegistryProvider docker_registry.IDockerRegistryProvider,
	kubernetesProvider kubernetes.IKubernetesProvider,
	cfg *config.Config,
) IMetricsProviderService {
	return &service{
		dockerProvider:         dockerProvider,
		dockerRegistryProvider: dockerRegistryProvider,
		kubernetesProvider:     kubernetesProvider,
		dockerHubNamespace:     cfg.DockerHubNamespace,
	}
}

func (s *service) UpMetricsProvider(ctx context.Context, namespace string, series int) error {
	const providerDockerfileContextPath = "./images/metrics-provider"

	tag, err := imageutil.BuildDevTag(providerDockerfileContextPath, "metrics-provider", s.dockerHubNamespace)
	if err != nil {
		return err
	}

	if err := s.dockerProvider.RecreateImageWithNewTag(ctx, tag, providerDockerfileContextPath); err != nil {
		return err
	}

	if err := s.dockerRegistryProvider.PushImage(ctx, tag); err != nil {
		return err
	}

	if err := s.kubernetesProvider.RecreateNamespace(ctx, namespace); err != nil {
		return err
	}

	if err := s.kubernetesProvider.CreateMetricsExporterDeployment(ctx, namespace, tag, series); err != nil {
		return err
	}

	if err := s.kubernetesProvider.CreateService(ctx, namespace); err != nil {
		return err
	}

	return nil
}
