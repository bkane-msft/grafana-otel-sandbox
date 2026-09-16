package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	if err := run(); err != nil {
		log.Fatalln(err)
	}
}

func run() (err error) {
	// Handle SIGINT (CTRL+C) gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Set up OpenTelemetry: logs, then metrics, then traces.
	// Each returns a shutdown function; defer them so nothing leaks. setup*
	// return the built logger/providers/propagator; main both registers them as
	// the process-wide globals (slog.SetDefault, otel.Set*) so global-reading
	// code works like a normal OTel app, and injects them into the server so
	// request handling stays testable without touching globals.
	logger, shutdownLogs, err := setupLogs(ctx)
	if err != nil {
		return
	}
	defer func() {
		err = errors.Join(err, shutdownLogs(context.Background()))
	}()
	slog.SetDefault(logger)

	meterProvider, shutdownMetrics, err := setupMetrics(ctx)
	if err != nil {
		return
	}
	defer func() {
		err = errors.Join(err, shutdownMetrics(context.Background()))
	}()
	otel.SetMeterProvider(meterProvider)

	tracerProvider, propagator, shutdownTraces, err := setupTraces(ctx)
	if err != nil {
		return
	}
	defer func() {
		err = errors.Join(err, shutdownTraces(context.Background()))
	}()
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagator)

	// Build the server from explicit providers. Passing nil for the roll
	// function uses the production default (real slow/random roll); the logger is
	// injected so the handler logs through the same logger set as the default.
	srv, err := newRollDiceServer(tracerProvider, meterProvider, nil, logger)
	if err != nil {
		return
	}

	// Start HTTP server.
	httpServer := &http.Server{
		Addr:         ":8081",
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
		ReadTimeout:  time.Second,
		WriteTimeout: 10 * time.Second,
		Handler:      newHTTPHandler(srv, tracerProvider, meterProvider, propagator),
	}
	srvErr := make(chan error, 1)
	go func() {
		srvErr <- httpServer.ListenAndServe()
	}()

	// Wait for interruption.
	select {
	case err = <-srvErr:
		// Error when starting HTTP server.
		return
	case <-ctx.Done():
		// Wait for first CTRL+C.
		// Stop receiving signal notifications as soon as possible.
		stop()
	}

	// When Shutdown is called, ListenAndServe immediately returns ErrServerClosed.
	err = httpServer.Shutdown(context.Background())
	return
}

// newHTTPHandler wires the /rolldice route. otelhttp defaults to the global
// providers/propagator, so we pass them explicitly to keep app behavior free of
// global OpenTelemetry state.
func newHTTPHandler(
	srv *RollDiceServer,
	tp trace.TracerProvider,
	mp metric.MeterProvider,
	propagator propagation.TextMapPropagator,
) http.Handler {
	mux := http.NewServeMux()

	// Register handlers.
	// Wrap each handler with otelhttp.NewHandler so that the http.route
	// attribute is set correctly from r.Pattern (populated by ServeMux).
	mux.Handle("/rolldice", otelhttp.NewHandler(
		http.HandlerFunc(srv.rolldice),
		"/rolldice",
		otelhttp.WithTracerProvider(tp),
		otelhttp.WithMeterProvider(mp),
		otelhttp.WithPropagators(propagator),
	))

	return mux
}
