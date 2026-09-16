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
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const schemaName = "https://github.com/grafana/docker-otel-lgtm"

// setupLogs builds the application slog.Logger and, unless using the local
// slogtint handler, the OpenTelemetry log pipeline. The destination is selected
// via OTEL_LOGS_EXPORTER:
//   - slogtint: colorized human-readable logs to stderr (no export, no-op shutdown)
//   - otlp/console/etc: slog records flow through the otelslog bridge to the
//     exporter chosen by autoexport (defaults to otlp)
//
// In all cases the verbosity is controlled by LOG_LEVEL (defaults to info).
//
// The built logger is returned rather than installed here: main calls
// slog.SetDefault on it (a global) and also injects it into the RollDiceServer,
// so request handling uses the injected logger while the rest of the process
// still gets a sensible default. The log pipeline's global logger provider is
// wired here because it is inseparable from building the provider.
func setupLogs(ctx context.Context) (*slog.Logger, func(context.Context) error, error) {
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

		logger := slog.New(tint.NewTextHandler(os.Stderr, &tint.Options{
			Level: level,
		}))
		return logger, func(context.Context) error { return nil }, nil
	}

	logExporter, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, nil, err
	}

	// minsev drops records below LOG_LEVEL and short-circuits the otelslog
	// bridge's Enabled check. Empty or unknown values fall back to INFO.
	var sev minsev.Severity
	_ = sev.UnmarshalText([]byte(os.Getenv("LOG_LEVEL")))

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(minsev.NewLogProcessor(sdklog.NewBatchProcessor(logExporter), sev)),
	)
	global.SetLoggerProvider(loggerProvider)

	logger := slog.New(otelslog.NewHandler(schemaName, otelslog.WithLoggerProvider(loggerProvider)))

	return logger, loggerProvider.Shutdown, nil
}

// setupMetrics configures the OpenTelemetry metric pipeline and starts Go
// runtime instrumentation. The metric reader is selected via
// OTEL_METRICS_EXPORTER (defaults to otlp); for otlp it wraps a PeriodicReader
// honoring OTEL_METRIC_EXPORT_INTERVAL.
//
// The provider is returned rather than installed here. main both registers it
// globally (otel.SetMeterProvider) so global-reading libraries work, and injects
// it into the RollDiceServer so request handling stays testable without globals.
// The returned function shuts the provider down.
func setupMetrics(ctx context.Context) (*sdkmetric.MeterProvider, func(context.Context) error, error) {
	metricReader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))

	// collect runtime metrics @ 1s instead of the default 15s for demo purposes.
	// Pass the meter provider explicitly instead of relying on the global one.
	if err := runtime.Start(
		runtime.WithMeterProvider(meterProvider),
		runtime.WithMinimumReadMemStatsInterval(time.Second),
	); err != nil {
		slog.ErrorContext(ctx, "otel runtime instrumentation failed:", slog.Any("error", err))
	}

	return meterProvider, meterProvider.Shutdown, nil
}

// setupTraces configures the OpenTelemetry trace pipeline. The span exporter is
// selected via OTEL_TRACES_EXPORTER (defaults to otlp) and propagators via
// OTEL_PROPAGATORS (defaults to tracecontext,baggage).
//
// The provider and propagator are returned rather than installed here. main both
// registers them globally (otel.SetTracerProvider / otel.SetTextMapPropagator)
// and injects them into the RollDiceServer and otelhttp, so request handling
// stays testable without globals. The returned function shuts the provider down.
func setupTraces(ctx context.Context) (*sdktrace.TracerProvider, propagation.TextMapPropagator, func(context.Context) error, error) {
	propagator := autoprop.NewTextMapPropagator()

	traceExporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, nil, nil, err
	}

	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traceExporter))

	return tracerProvider, propagator, tracerProvider.Shutdown, nil
}
