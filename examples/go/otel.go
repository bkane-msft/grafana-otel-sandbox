package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/contrib/processors/minsev"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
)

const schemaName = "https://github.com/grafana/docker-otel-lgtm"

var (
	tracer = otel.Tracer(schemaName)
	meter  = otel.Meter(schemaName)
)

// setupLogs configures the slog default logger and, unless using the local
// slogtint handler, the OpenTelemetry log pipeline. The destination is selected
// via OTEL_LOGS_EXPORTER:
//   - slogtint: colorized human-readable logs to stderr (no export, no-op shutdown)
//   - otlp/console/etc: slog records flow through the otelslog bridge to the
//     exporter chosen by autoexport (defaults to otlp)
//
// In all cases the verbosity is controlled by LOG_LEVEL (defaults to info).
func setupLogs(ctx context.Context) (func(context.Context) error, error) {
	// Local dev: pretty, colorized logs to stderr. Nothing is buffered or
	// exported, so shutdown has nothing to do.
	if os.Getenv("OTEL_LOGS_EXPORTER") == "slogtint" {
		level := map[string]slog.Level{
			"DEBUG": slog.LevelDebug,
			"INFO":  slog.LevelInfo,
			"WARN":  slog.LevelWarn,
			"ERROR": slog.LevelError,
			"":      slog.LevelInfo,
		}[strings.ToUpper(os.Getenv("LOG_LEVEL"))]

		slog.SetDefault(slog.New(tint.NewTextHandler(os.Stderr, &tint.Options{
			Level: level,
		})))
		return func(context.Context) error { return nil }, nil
	}

	logExporter, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, err
	}

	// minsev drops records below LOG_LEVEL and short-circuits the otelslog
	// bridge's Enabled check. Empty or unknown values fall back to INFO.
	var sev minsev.Severity
	_ = sev.UnmarshalText([]byte(os.Getenv("LOG_LEVEL")))

	loggerProvider := log.NewLoggerProvider(
		log.WithProcessor(minsev.NewLogProcessor(log.NewBatchProcessor(logExporter), sev)),
	)
	global.SetLoggerProvider(loggerProvider)

	slog.SetDefault(slog.New(otelslog.NewHandler(schemaName, otelslog.WithLoggerProvider(loggerProvider))))

	return loggerProvider.Shutdown, nil
}

// setupMetrics configures the OpenTelemetry metric pipeline, registers the
// global MeterProvider, and starts Go runtime instrumentation. The metric
// reader is selected via OTEL_METRICS_EXPORTER (defaults to otlp); for otlp it
// wraps a PeriodicReader honoring OTEL_METRIC_EXPORT_INTERVAL. The returned
// function shuts the provider down.
func setupMetrics(ctx context.Context) (func(context.Context) error, error) {
	metricReader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, err
	}

	meterProvider := metric.NewMeterProvider(metric.WithReader(metricReader))
	otel.SetMeterProvider(meterProvider)

	if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second)); err != nil {
		slog.ErrorContext(ctx, "otel runtime instrumentation failed:", slog.Any("error", err))
	}

	return meterProvider.Shutdown, nil
}

// setupTraces configures the OpenTelemetry trace pipeline, sets the text map
// propagator, and registers the global TracerProvider. Propagators are selected
// via OTEL_PROPAGATORS (defaults to tracecontext,baggage) and the span exporter
// via OTEL_TRACES_EXPORTER (defaults to otlp). The returned function shuts the
// provider down.
func setupTraces(ctx context.Context) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(autoprop.NewTextMapPropagator())

	traceExporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}

	tracerProvider := trace.NewTracerProvider(trace.WithBatcher(traceExporter))
	otel.SetTracerProvider(tracerProvider)

	return tracerProvider.Shutdown, nil
}
