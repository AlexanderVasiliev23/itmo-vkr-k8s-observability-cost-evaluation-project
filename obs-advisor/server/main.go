package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"obs-advisor-server/capacitymodel"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "", "путь до SQLite-базы obs-bench (обязательный)")
	port := flag.String("port", "8080", "порт HTTP-сервера")
	staticDir := flag.String("static", ".", "директория со статическими файлами (index.html и т.п.)")
	flag.Parse()

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "error: --db обязательный флаг")
		os.Exit(1)
	}

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		slog.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		slog.Error("ping db", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/measurements", func(w http.ResponseWriter, r *http.Request) {
		rows, err := loadRows(db)
		if err != nil {
			slog.Error("load rows", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rows)
	})

	mux.HandleFunc("/api/estimate", func(w http.ResponseWriter, r *http.Request) {
		rows, err := loadRows(db)
		if err != nil {
			slog.Error("load rows", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		q := r.URL.Query()
		targetLoad, _ := strconv.ParseFloat(q.Get("target_load"), 64)
		targetRetention, _ := strconv.ParseFloat(q.Get("target_retention_days"), 64)
		errorBudget, _ := strconv.ParseFloat(q.Get("error_budget"), 64)
		if errorBudget == 0 {
			errorBudget = capacitymodel.QualityTargetMAPEDisk
		}
		priceRAM, _ := strconv.ParseFloat(q.Get("price_ram"), 64)
		priceCPU, _ := strconv.ParseFloat(q.Get("price_cpu"), 64)
		priceDisk, _ := strconv.ParseFloat(q.Get("price_disk"), 64)

		report, err := capacitymodel.BuildReport(rows, capacitymodel.EstimateInput{
			Instrument:           q.Get("instrument"),
			WorkloadType:         q.Get("workload_type"),
			TargetLoad:           targetLoad,
			TargetRetentionDays:  targetRetention,
			ErrorBudget:          errorBudget,
			PriceRAMPerGiBMonth:  priceRAM,
			PriceCPUPerCoreMonth: priceCPU,
			PriceDiskPerGiBMonth: priceDisk,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(report)
	})

	mux.Handle("/", http.FileServer(http.Dir(*staticDir)))

	addr := ":" + *port
	srv := &http.Server{Addr: addr, Handler: mux}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("obs-advisor запущен", "addr", "http://localhost"+addr, "db", *dbPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	<-stop
	slog.Info("получен сигнал остановки, завершаем работу…")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("graceful shutdown", "err", err)
	}
	slog.Info("сервер остановлен")

}

func loadRows(db *sql.DB) ([]capacitymodel.Row, error) {
	rows, err := db.Query(`
		SELECT instrument, workload_type, load_value, retention_days, duration_seconds,
		       cpu_cores, mem_peak_bytes, disk_bytes
		FROM resource_usage_info
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []capacitymodel.Row
	for rows.Next() {
		var r capacitymodel.Row
		if err := rows.Scan(
			&r.Instrument, &r.WorkloadType, &r.LoadValue, &r.RetentionDays, &r.DurationSeconds,
			&r.CPUCores, &r.MemPeakBytes, &r.DiskBytes,
		); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
