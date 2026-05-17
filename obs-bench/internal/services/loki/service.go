package loki

import (
	"context"
	"fmt"
	"time"

	"obs-bench/internal/config"
	"obs-bench/internal/pkg/diskexporter"
	"obs-bench/internal/providers/docker"
	docker_registry "obs-bench/internal/providers/docker-registry"
	"obs-bench/internal/providers/helm"
	"obs-bench/internal/providers/kubernetes"
)

type ILokiService interface {
	UpLokiStack(ctx context.Context, namespace string, retentionDays int) error
}

type service struct {
	kubernetesProvider     kubernetes.IKubernetesProvider
	helmProvider           helm.IHelmProvider
	dockerProvider         docker.IDockerProvider
	dockerRegistryProvider docker_registry.IDockerRegistryProvider
	releaseName            string
	dockerHubNamespace     string
}

func NewLokiService(
	kubernetesProvider kubernetes.IKubernetesProvider,
	helmProvider helm.IHelmProvider,
	dockerProvider docker.IDockerProvider,
	dockerRegistryProvider docker_registry.IDockerRegistryProvider,
	cfg *config.Config,
) ILokiService {
	return &service{
		kubernetesProvider:     kubernetesProvider,
		helmProvider:           helmProvider,
		dockerProvider:         dockerProvider,
		dockerRegistryProvider: dockerRegistryProvider,
		releaseName:            cfg.Topology.Loki.HelmReleaseName,
		dockerHubNamespace:     cfg.DockerHubNamespace,
	}
}

func (s *service) UpLokiStack(ctx context.Context, namespace string, retentionDays int) error {
	if err := s.helmProvider.TryUninstall(ctx, s.releaseName); err != nil {
		return err
	}
	if err := s.kubernetesProvider.DeleteNamespace(ctx, namespace); err != nil {
		return err
	}

	const (
		repoURL   = "https://grafana.github.io/helm-charts"
		chartName = "loki"
	)

	vals := map[string]interface{}{
		"deploymentMode":   "SingleBinary",
		"fullnameOverride": s.releaseName,
		"loki": map[string]interface{}{
			"auth_enabled": false,
			"commonConfig": map[string]interface{}{
				"replication_factor": 1,
			},
			"storage": map[string]interface{}{
				"type": "filesystem",
			},
			"compactor": map[string]interface{}{
				"retention_enabled":    true,
				"delete_request_store": "filesystem",
			},
			"limits_config": map[string]interface{}{
				"retention_period": fmt.Sprintf("%dh", retentionDays*24),
			},
			"schemaConfig": map[string]interface{}{
				"configs": []interface{}{
					map[string]interface{}{
						"from":         "2024-04-01",
						"store":        "tsdb",
						"object_store": "filesystem",
						"schema":       "v13",
						"index": map[string]interface{}{
							"prefix": "loki_index_",
							"period": "24h",
						},
					},
				},
			},
		},
		"singleBinary": map[string]interface{}{
			"replicas": 1,
			"persistence": map[string]interface{}{
				"enabled": true,
				"size":    "10Gi",
			},
			"resources": map[string]interface{}{
				"limits": map[string]interface{}{
					"memory": "3Gi",
					"cpu":    "2",
				},
				"requests": map[string]interface{}{
					"memory": "512Mi",
					"cpu":    "250m",
				},
			},
		},
		"read": map[string]interface{}{
			"replicas": 0,
		},
		"write": map[string]interface{}{
			"replicas": 0,
		},
		"backend": map[string]interface{}{
			"replicas": 0,
		},
		"gateway": map[string]interface{}{
			"enabled": false,
		},
		"chunksCache": map[string]interface{}{
			"enabled": false,
		},
		"resultsCache": map[string]interface{}{
			"enabled": false,
		},
	}

	if err := s.helmProvider.Up(ctx, namespace, vals, repoURL, chartName, s.releaseName); err != nil {
		return err
	}

	if err := s.kubernetesProvider.CreateServiceMonitor(ctx, namespace, s.releaseName+"-metrics", "http",
		map[string]string{
			"app.kubernetes.io/name":     "loki",
			"app.kubernetes.io/instance": s.releaseName,
		}); err != nil {
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
	pvcName, err := s.kubernetesProvider.WaitForLokiSingleBinaryDataPVC(ctx, namespace, 5*time.Minute)
	if err != nil {
		return err
	}
	if err := s.kubernetesProvider.CreateDiskMetricsExporter(ctx, namespace, tag, pvcName); err != nil {
		return err
	}
	if err := s.kubernetesProvider.CreateDiskMetricsService(ctx, namespace); err != nil {
		return err
	}
	return s.kubernetesProvider.CreateServiceMonitor(ctx, namespace, "disk-metrics-exporter", "metrics",
		map[string]string{"app": "disk-metrics-exporter"})
}
