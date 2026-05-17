package capacitymodel

import (
	"fmt"
	"math"
	"sort"
)

const (
	secondsPerDay = 86400
	gib           = 1 << 30 // 1073741824
)

var ResourceKeys = []string{"cpu_cores", "mem_peak_bytes", "disk_bytes"}

const (
	QualityTargetMAPECPU     = 0.2
	QualityTargetMAPEMemPeak = 0.2
	QualityTargetMAPEDisk    = 0.2
)

type Row struct {
	Instrument      string  `json:"instrument"`
	WorkloadType    string  `json:"workload_type"`
	LoadValue       float64 `json:"load_value"`
	RetentionDays   float64 `json:"retention_days"`
	DurationSeconds float64 `json:"duration_seconds"`
	CPUCores        float64 `json:"cpu_cores"`
	MemPeakBytes    float64 `json:"mem_peak_bytes"`
	DiskBytes       float64 `json:"disk_bytes"`
}

type EstimateInput struct {
	Instrument           string
	WorkloadType         string
	TargetLoad           float64
	TargetRetentionDays  float64
	ErrorBudget          float64
	PriceRAMPerGiBMonth  float64
	PriceCPUPerCoreMonth float64
	PriceDiskPerGiBMonth float64
}

func keyVal(r Row, key string) float64 {
	switch key {
	case "cpu_cores":
		return r.CPUCores
	case "mem_peak_bytes":
		return r.MemPeakBytes
	case "disk_bytes":
		return r.DiskBytes
	default:
		return 0
	}
}

func interpolate(points [][2]float64, x float64) float64 {
	if len(points) == 0 {
		return 0
	}
	sort.Slice(points, func(i, j int) bool { return points[i][0] < points[j][0] })
	if len(points) == 1 {
		return points[0][1]
	}
	if x <= points[0][0] {
		return segment(points[0], points[1], x)
	}
	if x >= points[len(points)-1][0] {
		return segment(points[len(points)-2], points[len(points)-1], x)
	}
	i := 0
	for i < len(points)-1 && points[i+1][0] < x {
		i++
	}
	return segment(points[i], points[i+1], x)
}

func segment(p0, p1 [2]float64, x float64) float64 {
	dx := p1[0] - p0[0]
	if dx == 0 {
		return p0[1]
	}
	return p0[1] + ((x-p0[0])/dx)*(p1[1]-p0[1])
}

func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sort.Float64s(vals)
	n := len(vals)
	if n%2 == 0 {
		return (vals[n/2-1] + vals[n/2]) / 2
	}
	return vals[n/2]
}

func measuredPeriodDays(r Row) float64 {
	if r.DurationSeconds > 0 {
		return r.DurationSeconds / secondsPerDay
	}
	return r.RetentionDays
}

// CPU и RAM — интерполяция по нагрузке на основе коротких прогонов (до 1 дня),
// так как 24h-калибровочные прогоны моделируют долговременное дисковое поведение,
// а не пиковые CPU/RAM, характерные для основного окна измерений после прогрева.
// Для disk_bytes используются все прогоны, включая калибровочные 24h.
func rowsForResourceModel(rows []Row, resourceKey string) []Row {
	if resourceKey == "disk_bytes" {
		return rows
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if r.DurationSeconds < secondsPerDay {
			out = append(out, r)
		}
	}
	return out
}

func deduplicateByLoad(points [][2]float64) [][2]float64 {
	byLoad := map[float64][]float64{}
	var order []float64
	seen := map[float64]bool{}
	for _, p := range points {
		if !seen[p[0]] {
			order = append(order, p[0])
			seen[p[0]] = true
		}
		byLoad[p[0]] = append(byLoad[p[0]], p[1])
	}
	out := make([][2]float64, 0, len(byLoad))
	for _, load := range order {
		out = append(out, [2]float64{load, median(byLoad[load])})
	}
	return out
}

func estimateAlphaByRetention(rows []Row, resourceKey string) float64 {
	byLoad := map[float64][]Row{}
	for _, r := range rows {
		if keyVal(r, resourceKey) > 0 {
			byLoad[r.LoadValue] = append(byLoad[r.LoadValue], r)
		}
	}

	type pt struct{ x, y float64 }
	var alphas []float64
	for _, loadRows := range byLoad {
		if len(loadRows) < 2 {
			continue
		}
		var pts []pt
		for _, r := range loadRows {
			t := measuredPeriodDays(r)
			d := keyVal(r, resourceKey)
			if t > 0 && d > 0 {
				pts = append(pts, pt{math.Log(t), math.Log(d)})
			}
		}
		if len(pts) < 2 {
			continue
		}
		var xMean, yMean float64
		for _, p := range pts {
			xMean += p.x
			yMean += p.y
		}
		xMean /= float64(len(pts))
		yMean /= float64(len(pts))
		var cov, vr float64
		for _, p := range pts {
			cov += (p.x - xMean) * (p.y - yMean)
			vr += (p.x - xMean) * (p.x - xMean)
		}
		if vr == 0 {
			continue
		}
		alphas = append(alphas, cov/vr)
	}

	if len(alphas) == 0 {
		return 1.0
	}
	// Ограничиваем α в [0.6, 1.4] — степенной закон роста данных по retention
	// не может быть сублинейнее 0.6 (too optimistic) или сверхлинейнее 1.4 (too pessimistic)
	return math.Max(0.6, math.Min(1.4, median(alphas)))
}

func estimateResource(rows []Row, targetLoad, targetRetention float64, resourceKey string) float64 {
	modelRows := rowsForResourceModel(rows, resourceKey)
	points := make([][2]float64, 0, len(modelRows))

	var alpha float64
	if resourceKey == "disk_bytes" {
		alpha = estimateAlphaByRetention(modelRows, resourceKey)
	}

	for _, r := range modelRows {
		base := keyVal(r, resourceKey)
		if base <= 0 {
			continue
		}
		scaled := base
		if resourceKey == "disk_bytes" {
			scaled = base * math.Pow(targetRetention/measuredPeriodDays(r), alpha)
		}
		points = append(points, [2]float64{r.LoadValue, scaled})
	}
	return math.Max(0.0, interpolate(deduplicateByLoad(points), targetLoad))
}

func estimateAll(rows []Row, targetLoad, targetRetention float64) map[string]float64 {
	return map[string]float64{
		"cpu_cores":      estimateResource(rows, targetLoad, targetRetention, "cpu_cores"),
		"mem_peak_bytes": estimateResource(rows, targetLoad, targetRetention, "mem_peak_bytes"),
		"disk_bytes":     estimateResource(rows, targetLoad, targetRetention, "disk_bytes"),
	}
}

func mape(yTrue, yPred float64) float64 {
	if yTrue <= 0 {
		return 0
	}
	return math.Abs(yTrue-yPred) / yTrue
}

func validateHoldout(rows []Row) map[string]float64 {
	out := map[string]float64{
		"cpu_cores":      math.NaN(),
		"mem_peak_bytes": math.NaN(),
		"disk_bytes":     math.NaN(),
	}
	if len(rows) < 3 {
		return out
	}

	errs := map[string][]float64{
		"cpu_cores":      {},
		"mem_peak_bytes": {},
		"disk_bytes":     {},
	}
	for i, test := range rows {
		train := make([]Row, 0, len(rows)-1)
		train = append(train, rows[:i]...)
		train = append(train, rows[i+1:]...)
		if len(train) < 2 {
			continue
		}

		if test.DurationSeconds >= secondsPerDay {
			continue
		}
		pred := estimateAll(train, test.LoadValue, test.RetentionDays)
		predDisk := estimateResource(train, test.LoadValue, measuredPeriodDays(test), "disk_bytes")
		errs["cpu_cores"] = append(errs["cpu_cores"], mape(test.CPUCores, pred["cpu_cores"]))
		errs["mem_peak_bytes"] = append(errs["mem_peak_bytes"], mape(test.MemPeakBytes, pred["mem_peak_bytes"]))
		errs["disk_bytes"] = append(errs["disk_bytes"], mape(test.DiskBytes, predDisk))
	}
	for _, k := range ResourceKeys {
		if len(errs[k]) == 0 {
			continue
		}
		var s float64
		for _, v := range errs[k] {
			s += v
		}
		out[k] = s / float64(len(errs[k]))
	}
	return out
}

func sanitizeNaNMap(in map[string]float64) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			out[k] = nil
			continue
		}
		out[k] = v
	}
	return out
}

func formatBytes(n float64) string {
	return fmt.Sprintf("%.0f bytes (%.2f GiB)", n, n/float64(gib))
}

func BuildReport(rows []Row, in EstimateInput) (map[string]any, error) {
	subset := make([]Row, 0)
	for _, r := range rows {
		if r.Instrument == in.Instrument && r.WorkloadType == in.WorkloadType {
			subset = append(subset, r)
		}
	}
	if len(subset) == 0 {
		return nil, fmt.Errorf("no rows for instrument=%s, workload_type=%s", in.Instrument, in.WorkloadType)
	}

	estimate := estimateAll(subset, in.TargetLoad, in.TargetRetentionDays)
	holdout := validateHoldout(subset)

	rangeOut := map[string]map[string]float64{}
	for _, k := range ResourceKeys {
		rangeOut[k] = map[string]float64{
			"min": estimate[k] * (1.0 - in.ErrorBudget),
			"max": estimate[k] * (1.0 + in.ErrorBudget),
		}
	}

	out := map[string]any{
		"target": map[string]any{
			"instrument":     in.Instrument,
			"workload_type":  in.WorkloadType,
			"load_value":     in.TargetLoad,
			"retention_days": in.TargetRetentionDays,
		},
		"estimate": estimate,
		"estimate_human": map[string]any{
			"cpu_cores":      math.Round(estimate["cpu_cores"]*10000) / 10000,
			"mem_peak_bytes": formatBytes(estimate["mem_peak_bytes"]),
			"disk_bytes":     formatBytes(estimate["disk_bytes"]),
		},
		"validation_mape": sanitizeNaNMap(holdout),
		"quality_targets": map[string]float64{
			"cpu_cores_mape_max":      QualityTargetMAPECPU,
			"mem_peak_bytes_mape_max": QualityTargetMAPEMemPeak,
			"disk_bytes_mape_max":     QualityTargetMAPEDisk,
		},
		"range_with_error_budget": rangeOut,
	}

	if in.PriceRAMPerGiBMonth > 0 || in.PriceCPUPerCoreMonth > 0 || in.PriceDiskPerGiBMonth > 0 {
		ramGiB := estimate["mem_peak_bytes"] / gib
		diskGiB := estimate["disk_bytes"] / gib
		cpuCores := estimate["cpu_cores"]

		costRAM := ramGiB * in.PriceRAMPerGiBMonth
		costCPU := cpuCores * in.PriceCPUPerCoreMonth
		costDisk := diskGiB * in.PriceDiskPerGiBMonth

		ramGiBMin := rangeOut["mem_peak_bytes"]["min"] / gib
		ramGiBMax := rangeOut["mem_peak_bytes"]["max"] / gib
		cpuMin := rangeOut["cpu_cores"]["min"]
		cpuMax := rangeOut["cpu_cores"]["max"]
		diskGiBMin := rangeOut["disk_bytes"]["min"] / gib
		diskGiBMax := rangeOut["disk_bytes"]["max"] / gib

		out["cost"] = map[string]float64{
			"ram":       costRAM,
			"cpu":       costCPU,
			"disk":      costDisk,
			"total":     costRAM + costCPU + costDisk,
			"total_min": ramGiBMin*in.PriceRAMPerGiBMonth + cpuMin*in.PriceCPUPerCoreMonth + diskGiBMin*in.PriceDiskPerGiBMonth,
			"total_max": ramGiBMax*in.PriceRAMPerGiBMonth + cpuMax*in.PriceCPUPerCoreMonth + diskGiBMax*in.PriceDiskPerGiBMonth,
		}
	}

	return out, nil
}
