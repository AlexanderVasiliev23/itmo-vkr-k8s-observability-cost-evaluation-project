package diskexporter

import "obs-bench/internal/pkg/imageutil"

const ContextPath = "./images/disk-usage-metrics-exporter"

func BuildDevImageTag(namespace string) (string, error) {
	return imageutil.BuildDevTag(ContextPath, "disk-usage-metrics-exporter", namespace)
}
