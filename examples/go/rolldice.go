package main

import (
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func rolldice(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "roll")
	defer span.End()

	roll := 1 + roll()

	slog.InfoContext(ctx, "Rolled a dice", slog.Int("result", roll))

	resp := strconv.Itoa(roll) + "\n"
	if _, err := io.WriteString(w, resp); err != nil {
		slog.ErrorContext(ctx, "Write failed", slog.Any("error", err))
	}

	h, err := meter.Int64Histogram("dice.roll", metric.WithDescription("The result of the dice roll"))
	success := (err == nil)
	if !success {
		slog.ErrorContext(ctx, "Histogram instantiation failed", slog.Any("error", err))
	}
	h.Record(ctx, int64(roll), metric.WithAttributes(attribute.Bool("result.success", success)))
}

func roll() int {
	// simulate a long operation
	// busy wait to make sure it's shown in the flame graph
	start := time.Now()
	//nolint:revive // intentional busy wait for flame graph demo
	for time.Since(start) < 1*time.Second {
	}

	//nolint:gosec
	return rand.Intn(6)
}
