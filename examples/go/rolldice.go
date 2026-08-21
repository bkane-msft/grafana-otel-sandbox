package main

import (
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	diceRollHistogram metric.Int64Histogram
	diceRollSuccesses metric.Int64Counter
)

func setupRollDiceMetrics(meter metric.Meter) error {
	var err error
	if diceRollHistogram, err = meter.Int64Histogram(
		"dice.roll.result",
		metric.WithDescription("The result of a successful dice roll"),
		metric.WithUnit("{roll}"),
		metric.WithExplicitBucketBoundaries(1, 2, 3, 4, 5, 6),
	); err != nil {
		return err
	}

	if diceRollSuccesses, err = meter.Int64Counter(
		"dice.roll.successes",
		metric.WithDescription("The number of successful dice rolls"),
		metric.WithUnit("{rolls}"),
	); err != nil {
		return err
	}
	return nil
}

func rolldice(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "roll", trace.WithAttributes(attribute.String("key", "value")), trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()

	// top level error handling - https://www.zombiezen.com/blog/2026/07/wrapping-errors-with-defer/
	var err error
	defer func() {
		if err != nil {
			slog.ErrorContext(ctx, "rollDice Error", slog.Any("error", err))
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	}()

	var diceRoll int
	diceRoll, err = roll()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	slog.InfoContext(ctx, "Rolled a dice", slog.Int("result", diceRoll))

	diceRollHistogram.Record(ctx, int64(diceRoll))
	diceRollSuccesses.Add(ctx, 1, metric.WithAttributes(attribute.Int("result", diceRoll)))

	resp := strconv.Itoa(diceRoll) + "\n"
	_, err = io.WriteString(w, resp)
	if err != nil {
		slog.ErrorContext(ctx, "Write failed", slog.Any("error", err))
	}

}

func roll() (int, error) {
	// simulate a long operation
	// busy wait to make sure it's shown in the flame graph
	start := time.Now()
	//nolint:revive // intentional busy wait for flame graph demo
	for time.Since(start) < 1*time.Second {
	}

	//nolint:gosec
	dice := rand.Intn(7)
	dice = dice + 1

	// randomly fail to demonstrate error handling
	if dice == 7 {
		return 0, fmt.Errorf("dice rolled a seven, which is not realistic")
	}
	return dice, nil
}
