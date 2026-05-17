package resourcepromql

import (
	"fmt"
	"regexp"

	"obs-bench/internal/config"
)

func cadvisorPodSelector(t config.InstrumentTarget) (podRE string, ok bool) {
	if t.HelmReleaseName != "" {
		return "^" + regexp.QuoteMeta(t.HelmReleaseName) + "-[0-9]+$", true
	}
	if t.QueryServiceName != "" {
		return "^" + regexp.QuoteMeta(t.QueryServiceName) + "-[0-9]+$", true
	}
	return "", false
}

func ResourceQueries(target config.InstrumentTarget, durSeconds int) (cpu, memAvg, memPeak, disk string, err error) {
	ns := target.PVCQueryNamespace
	// max_over_time: берём пик за окно сбора — защита от просадок при сжатии/compaction.
	disk = fmt.Sprintf(`max_over_time(sum(pvc_used_bytes{namespace="%s"})[%ds:30s])`, ns, durSeconds)

	if target.CadvisorPodSelector != "" {
		podRE := target.CadvisorPodSelector
		const selCPU = `namespace="%s",pod=~"%s",container!="",container!="POD",cpu="total"`
		const selMem = `namespace="%s",pod=~"%s",container!="",container!="POD"`
		cpu = fmt.Sprintf(
			`avg_over_time((sum(max by (namespace, pod, id) (rate(container_cpu_usage_seconds_total{`+selCPU+`}[1m]))))[%ds:15s])`,
			ns, podRE, durSeconds,
		)
		memAvg = fmt.Sprintf(
			`avg_over_time((sum(max by (namespace, pod, id) (container_memory_working_set_bytes{`+selMem+`})))[%ds:15s])`,
			ns, podRE, durSeconds,
		)
		memPeak = fmt.Sprintf(
			`max_over_time((sum(max by (namespace, pod, id) (container_memory_working_set_bytes{`+selMem+`})))[%ds:15s])`,
			ns, podRE, durSeconds,
		)
		return cpu, memAvg, memPeak, disk, nil
	}

	if target.CadvisorContainerName != "" {
		cn := target.CadvisorContainerName
		if podRE, ok := cadvisorPodSelector(target); ok {
			const selCPU = `namespace="%s",pod=~"%s",container="%s",container!="POD",cpu="total"`
			const selMem = `namespace="%s",pod=~"%s",container="%s"`
			cpu = fmt.Sprintf(
				`avg_over_time((sum(max by (namespace, pod, id) (rate(container_cpu_usage_seconds_total{`+selCPU+`}[1m]))))[%ds:15s])`,
				ns, podRE, cn, durSeconds,
			)
			memAvg = fmt.Sprintf(
				`avg_over_time((sum(max by (namespace, pod, id) (container_memory_working_set_bytes{`+selMem+`})))[%ds:15s])`,
				ns, podRE, cn, durSeconds,
			)
			memPeak = fmt.Sprintf(
				`max_over_time((sum(max by (namespace, pod, id) (container_memory_working_set_bytes{`+selMem+`})))[%ds:15s])`,
				ns, podRE, cn, durSeconds,
			)
			return cpu, memAvg, memPeak, disk, nil
		}
		const selCPU = `namespace="%s",container="%s",cpu="total"`
		const selMem = `namespace="%s",container="%s"`
		cpu = fmt.Sprintf(
			`avg_over_time((sum by (namespace, pod) (max by (namespace, pod, id) (rate(container_cpu_usage_seconds_total{`+selCPU+`}[1m]))))[%ds:15s])`,
			ns, cn, durSeconds,
		)
		memAvg = fmt.Sprintf(
			`avg_over_time((sum by (namespace, pod) (max by (namespace, pod, id) (container_memory_working_set_bytes{`+selMem+`})))[%ds:15s])`,
			ns, cn, durSeconds,
		)
		memPeak = fmt.Sprintf(
			`max_over_time((sum by (namespace, pod) (max by (namespace, pod, id) (container_memory_working_set_bytes{`+selMem+`})))[%ds:15s])`,
			ns, cn, durSeconds,
		)
		return cpu, memAvg, memPeak, disk, nil
	}

	if target.ProcessMetricsJob == "" {
		return "", "", "", "", fmt.Errorf("instrument target: need ProcessMetricsJob or CadvisorContainerName")
	}

	job := target.ProcessMetricsJob
	cpu = fmt.Sprintf(`avg_over_time(rate(process_cpu_seconds_total{job="%s"}[1m])[%ds:15s])`, job, durSeconds)
	memAvg = fmt.Sprintf(`avg_over_time(process_resident_memory_bytes{job="%s"}[%ds])`, job, durSeconds)
	memPeak = fmt.Sprintf(`max_over_time(process_resident_memory_bytes{job="%s"}[%ds])`, job, durSeconds)
	return cpu, memAvg, memPeak, disk, nil
}
