package models

import (
	"time"

	"github.com/uptrace/bun"
)

type ResourcesUsageInfoModel struct {
	bun.BaseModel `bun:"table:resource_usage_info"`

	ID        int       `bun:",pk,autoincrement"`
	CreatedAt time.Time `bun:",notnull,default:current_timestamp"`

	// workload_type: "metrics" | "logs"
	WorkloadType string `bun:"workload_type,notnull"`
	// load_value: cardinality for metrics, logs/sec for logs.
	LoadValue int `bun:"load_value,notnull"`
	// retention_days: заданный горизонт хранения в сценарии эксперимента, 1/7/30.
	RetentionDays int `bun:"retention_days,notnull"`

	DurationSeconds int    `bun:"duration_seconds,notnull"`
	Instrument      string `bun:"instrument,notnull"`

	CPUCores     float64 `bun:"cpu_cores,notnull"`      // average CPU cores used during collection window
	MemAvgBytes  int64   `bun:"mem_avg_bytes,notnull"`  // average container_memory_working_set_bytes during collection window
	MemPeakBytes int64   `bun:"mem_peak_bytes,notnull"` // peak container_memory_working_set_bytes during collection window
	DiskBytes    int64   `bun:"disk_bytes,notnull"`     // bytes used by the data PVC during collection window
}
