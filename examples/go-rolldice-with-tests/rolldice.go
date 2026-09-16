package main

import (
	"errors"
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

// errSevenNotRealistic is returned by defaultRoll on its rare simulated failure.
var errSevenNotRealistic = errors.New("dice rolled a seven, which is not realistic")

// RollDiceServer owns all request-time dependencies for the /rolldice handler.
// Everything the handler needs is a field so tests can inject fakes without
// touching global state: a roll function (to control the outcome), a logger (to
// capture or discard output), a tracer, and the metric instruments.
type RollDiceServer struct {
	roll              func() (int, error)
	logger            *slog.Logger
	tracer            trace.Tracer
	diceRollHistogram metric.Int64Histogram
	diceRollSuccesses metric.Int64Counter
}

// newRollDiceServer builds a RollDiceServer from explicit OpenTelemetry
// providers instead of the global ones. Passing nil for roll or logger falls
// back to the production defaults (the real slow/random roll and slog.Default).
func newRollDiceServer(
	tp trace.TracerProvider,
	mp metric.MeterProvider,
	roll func() (int, error),
	logger *slog.Logger,
) (*RollDiceServer, error) {
	if roll == nil {
		roll = defaultRoll
	}
	if logger == nil {
		logger = slog.Default()
	}

	meter := mp.Meter(schemaName)
	histogram, successes, err := setupRollDiceMetrics(meter)
	if err != nil {
		return nil, err
	}

	return &RollDiceServer{
		roll:              roll,
		logger:            logger,
		tracer:            tp.Tracer(schemaName),
		diceRollHistogram: histogram,
		diceRollSuccesses: successes,
	}, nil
}

// setupRollDiceMetrics creates the dice metric instruments from the given meter
// and returns them. Unlike the go-global-state example, it does not mutate any
// package-level state.
func setupRollDiceMetrics(meter metric.Meter) (metric.Int64Histogram, metric.Int64Counter, error) {
	diceRollHistogram, err := meter.Int64Histogram(
		"dice.roll.result",
		metric.WithDescription("The result of a successful dice roll"),
		metric.WithUnit("{roll}"),
		metric.WithExplicitBucketBoundaries(1, 2, 3, 4, 5, 6),
	)
	if err != nil {
		return nil, nil, err
	}

	diceRollSuccesses, err := meter.Int64Counter(
		"dice.roll.successes",
		metric.WithDescription("The number of successful dice rolls"),
		metric.WithUnit("{rolls}"),
	)
	if err != nil {
		return nil, nil, err
	}

	return diceRollHistogram, diceRollSuccesses, nil
}

func (s *RollDiceServer) rolldice(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.tracer.Start(r.Context(), "roll", trace.WithAttributes(attribute.String("key", "value")), trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()

	// top level error handling - https://www.zombiezen.com/blog/2026/07/wrapping-errors-with-defer/
	var err error
	defer func() {
		if err != nil {
			s.logger.ErrorContext(ctx, "rollDice Error", slog.Any("error", err))
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	}()

	var diceRoll int
	diceRoll, err = s.roll()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.logger.InfoContext(ctx, "Rolled a dice", slog.Int("result", diceRoll))

	s.diceRollHistogram.Record(ctx, int64(diceRoll))
	s.diceRollSuccesses.Add(ctx, 1, metric.WithAttributes(attribute.Int("result", diceRoll)))

	resp := strconv.Itoa(diceRoll) + "\n"
	_, err = io.WriteString(w, resp)
	if err != nil {
		s.logger.ErrorContext(ctx, "Write failed", slog.Any("error", err))
	}

}

// defaultRoll is the production dice roller: intentionally slow (for the flame
// graph demo) and random, with a rare failure to exercise error handling.
func defaultRoll() (int, error) {
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
		return 0, errSevenNotRealistic
	}
	return dice, nil
}
