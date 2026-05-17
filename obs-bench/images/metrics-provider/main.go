package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	prometheusexporter "go.opentelemetry.io/otel/exporters/prometheus"
	metric2 "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
)

func main() {
	slog.Info("starting metrics provider")

	port := os.Getenv("PORT")
	if port == "" {
		log.Fatal("PORT env variable is required")
	}

	seriesCountStr := os.Getenv("SERIES_COUNT")
	if seriesCountStr == "" {
		log.Fatal("SERIES_COUNT env variable is required")
	}
	seriesCount, err := strconv.Atoi(seriesCountStr)
	if err != nil {
		log.Fatalf("invalid SERIES_COUNT: %v", err)
	}

	exporter, err := prometheusexporter.New()
	if err != nil {
		log.Fatal(err)
	}

	provider := metric.NewMeterProvider(metric.WithReader(exporter))
	defer provider.Shutdown(context.Background())
	otel.SetMeterProvider(provider)

	meter := otel.Meter("metrics-provider")

	gauge, err := meter.Float64ObservableGauge("bench_series_value",
		metric2.WithDescription("Simulated time-series value for cardinality benchmarking"),
	)
	if err != nil {
		log.Fatal(err)
	}

	values := make([]float64, seriesCount)
	for i := range seriesCount {
		values[i] = rand.Float64()
	}

	attrSets := make([]metric2.MeasurementOption, seriesCount)
	for i := range seriesCount {
		attrSets[i] = metric2.WithAttributes(attribute.String("series_id", fmt.Sprintf("series_%d", i)))
	}

	// Обновляем значения в фоне, чтобы метрики не были статичными.
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			for i := range values {
				values[i] = rand.Float64()
			}
		}
	}()

	_, err = meter.RegisterCallback(func(_ context.Context, o metric2.Observer) error {
		for i := range seriesCount {
			o.ObserveFloat64(gauge, values[i], attrSets[i])
		}
		return nil
	}, gauge)
	if err != nil {
		log.Fatal(err)
	}

	slog.Info("metrics provider started", "series_count", seriesCount)

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
