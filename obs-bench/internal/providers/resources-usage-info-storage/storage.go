package resources_usage_info_storage_provider

import (
	"context"
	"database/sql"
	"obs-bench/internal/models"
	"strings"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"
	"github.com/uptrace/bun/extra/bundebug"
)

type IResourcesUsageInfoStorageProvider interface {
	Save(ctx context.Context, info *models.ResourcesUsageInfoModel) error
	Close() error
}

type provider struct {
	db *bun.DB
}

func NewResourcesUsageInfoStorageProvider(ctx context.Context, dsn string, sqlDebug bool) (IResourcesUsageInfoStorageProvider, error) {
	sqlDB, err := sql.Open(sqliteshim.ShimName, dsn)
	if err != nil {
		return nil, err
	}
	db := bun.NewDB(sqlDB, sqlitedialect.New())
	if sqlDebug {
		db.AddQueryHook(bundebug.NewQueryHook())
	}

	if _, err := db.NewCreateTable().Model((*models.ResourcesUsageInfoModel)(nil)).IfNotExists().Exec(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := ensureResourcesUsageInfoSchemaMigrated(ctx, sqlDB); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &provider{
		db: db,
	}, nil
}

func ensureResourcesUsageInfoSchemaMigrated(ctx context.Context, sqlDB *sql.DB) error {
	cols, err := listColumns(ctx, sqlDB, "resource_usage_info")
	if err != nil {
		return err
	}

	if cols.has("workload_type") && cols.has("load_value") && cols.has("retention_days") {
		return nil
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.ExecContext(ctx, `DROP TABLE IF EXISTS resource_usage_info_new`)

	_, err = tx.ExecContext(ctx, `
		CREATE TABLE resource_usage_info_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			workload_type VARCHAR NOT NULL,
			load_value INTEGER NOT NULL,
			retention_days INTEGER NOT NULL,
			duration_seconds INTEGER NOT NULL,
			cpu_cores DOUBLE PRECISION NOT NULL,
			mem_avg_bytes INTEGER NOT NULL,
			mem_peak_bytes INTEGER NOT NULL,
			disk_bytes INTEGER NOT NULL,
			instrument VARCHAR NOT NULL
		);
	`)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO resource_usage_info_new (
			id, created_at, workload_type, load_value, retention_days,
			duration_seconds, cpu_cores, mem_avg_bytes, mem_peak_bytes, disk_bytes, instrument
		)
		SELECT
			id, created_at,
			CASE
				WHEN instrument IN ('loki', 'opensearch') THEN 'logs'
				ELSE 'metrics'
			END AS workload_type,
			series AS load_value,
			7 AS retention_days,
			duration_seconds,
			cpu_cores, mem_avg_bytes, mem_peak_bytes, disk_bytes, instrument
		FROM resource_usage_info;
	`)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DROP TABLE resource_usage_info`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE resource_usage_info_new RENAME TO resource_usage_info`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

type colSet map[string]struct{}

func (s colSet) has(name string) bool {
	_, ok := s[strings.ToLower(name)]
	return ok
}

func listColumns(ctx context.Context, sqlDB *sql.DB, table string) (colSet, error) {
	rows, err := sqlDB.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(colSet)
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   sql.NullString
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols[strings.ToLower(name)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

func (p *provider) Save(ctx context.Context, info *models.ResourcesUsageInfoModel) error {
	_, err := p.db.NewInsert().Model(info).Exec(ctx)

	if err != nil {
		return err
	}

	return nil
}

func (p *provider) Close() error {
	return p.db.Close()
}
