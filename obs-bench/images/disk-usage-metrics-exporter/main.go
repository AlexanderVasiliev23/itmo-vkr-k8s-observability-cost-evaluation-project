package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func getMountPath() string {
	if p := os.Getenv("MOUNT_PATH"); p != "" {
		return p
	}
	if p := os.Getenv("PVC_MOUNT_PATH"); p != "" {
		return p
	}
	return "/data"
}

func getPort() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8080"
}

func getCollectInterval() time.Duration {
	v := strings.TrimSpace(os.Getenv("COLLECT_INTERVAL_SECONDS"))
	if v == "" {
		slog.Error("COLLECT_INTERVAL_SECONDS env variable is required")
		os.Exit(1)
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		slog.Error("invalid COLLECT_INTERVAL_SECONDS: must be a positive integer", "value", v)
		os.Exit(1)
	}
	return time.Duration(secs) * time.Second
}

func parseLogLevel() slog.Level {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func statfsBytes(ctx context.Context, logger *slog.Logger, path string) (available, capacity uint64, err error) {
	var st syscall.Statfs_t
	if err = syscall.Statfs(path, &st); err != nil {
		logger.ErrorContext(ctx, "statfs failed", "path", path, "err", err)
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}

	blockSize := uint64(st.Bsize)
	capacity = blockSize * uint64(st.Blocks)
	available = blockSize * uint64(st.Bavail)
	free := blockSize * uint64(st.Bfree)

	logger.DebugContext(
		ctx,
		"statfs",
		"path", path,
		"type", st.Type,
		"bsize", st.Bsize,
		"blocks", st.Blocks,
		"bfree", st.Bfree,
		"bavail", st.Bavail,
		"files", st.Files,
		"ffree", st.Ffree,
		"namelen", st.Namelen,
		"frsize", st.Frsize,
		"flags", st.Flags,
		"capacity_bytes", capacity,
		"available_bytes", available,
		"free_bytes", free,
	)

	return available, capacity, nil
}

func duUsedBytes(path string) (uint64, error) {
	if out, err := exec.Command("du", "-s", "-B1", path).Output(); err == nil {
		return parseDUValue(out)
	}
	out, err := exec.Command("du", "-sk", path).Output()
	if err != nil {
		return 0, err
	}
	v, err := parseDUValue(out)
	if err != nil {
		return 0, err
	}
	return v * 1024, nil
}

func parseDUValue(out []byte) (uint64, error) {
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("пустой вывод du")
	}
	v, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("не удалось распарсить du %q: %w", fields[0], err)
	}
	return v, nil
}

type state struct {
	mu        sync.RWMutex
	used      uint64
	available uint64
	capacity  uint64
	up        float64
}

func (s *state) set(used, available, capacity uint64, up float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.used = used
	s.available = available
	s.capacity = capacity
	s.up = up
}

func (s *state) snapshot() (uint64, uint64, uint64, float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.used, s.available, s.capacity, s.up
}

func collectOnce(ctx context.Context, logger *slog.Logger, mountPath string) (used, available, capacity uint64, err error) {
	used, err = duUsedBytes(mountPath)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("du: %w", err)
	}
	available, capacity, err = statfsBytes(ctx, logger, mountPath)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("statfs: %w", err)
	}
	return used, available, capacity, nil
}

func runCollector(ctx context.Context, logger *slog.Logger, st *state, mountPath string, every time.Duration) {
	update := func() {
		used, available, capacity, err := collectOnce(ctx, logger, mountPath)
		if err != nil {
			logger.ErrorContext(ctx, "collect failed", "err", err, "mount_path", mountPath)
			st.set(0, 0, 0, 0)
			return
		}
		st.set(used, available, capacity, 1)
	}
	update()

	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for range ticker.C {
		update()
	}
}

func main() {
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel()})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	ctx := context.Background()
	logger.InfoContext(ctx, fmt.Sprintf("starting pvc-exporter, version: %s", "0.0.2"))

	mountPath := getMountPath()
	port := getPort()
	collectInterval := getCollectInterval()
	logger.InfoContext(ctx, "config", "mount_path", mountPath, "port", port, "collect_interval", collectInterval.String())

	st := &state{}
	go runCollector(ctx, logger, st, mountPath, collectInterval)

	pvcUsedBytes := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "pvc_used_bytes",
			Help: "Used bytes under PVC mount path from periodic du collection",
		},
		func() float64 {
			used, _, _, _ := st.snapshot()
			return float64(used)
		},
	)
	pvcAvailableBytes := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "pvc_available_bytes",
			Help: "Available bytes under PVC mount path from periodic statfs collection",
		},
		func() float64 {
			_, available, _, _ := st.snapshot()
			return float64(available)
		},
	)
	pvcCapacityBytes := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "pvc_capacity_bytes",
			Help: "Capacity in bytes of PVC mount path from periodic statfs collection",
		},
		func() float64 {
			_, _, capacity, _ := st.snapshot()
			return float64(capacity)
		},
	)
	collectorUp := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "pvc_collector_up",
			Help: "1 if last periodic PVC collection succeeded, otherwise 0",
		},
		func() float64 {
			_, _, _, up := st.snapshot()
			return up
		},
	)

	reg := prometheus.NewRegistry()
	reg.MustRegister(pvcUsedBytes, pvcAvailableBytes, pvcCapacityBytes, collectorUp)

	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/metrics", http.StatusMovedPermanently)
	})

	addr := ":" + port
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
}
