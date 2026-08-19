package main

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
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
	logger = otelslog.NewLogger(schemaName)
	meter  = otel.Meter(schemaName)
)

// setupLogs configures the OpenTelemetry log pipeline and registers the global
// LoggerProvider. The log exporter is selected via OTEL_LOGS_EXPORTER
// (defaults to otlp). The returned function shuts the provider down.
func setupLogs(ctx context.Context) (func(context.Context) error, error) {
	logExporter, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, err
	}

	loggerProvider := log.NewLoggerProvider(log.WithProcessor(log.NewBatchProcessor(logExporter)))
	global.SetLoggerProvider(loggerProvider)

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
		logger.ErrorContext(ctx, "otel runtime instrumentation failed:", slog.Any("error", err))
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
