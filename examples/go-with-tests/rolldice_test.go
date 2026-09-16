package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace/noop"
)

// safeBuffer is a concurrency-safe io.Writer that records everything written to
// it, so tests can assert on emitted log lines. It is safe even if the handler
// logs from multiple goroutines.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newTestServer builds a RollDiceServer wired to a manual metric reader and a
// log-recording buffer so tests can collect and assert on both metrics and log
// output. The tracer is a no-op, so no global OpenTelemetry state is touched.
func newTestServer(t *testing.T, roll func() (int, error)) (*RollDiceServer, *metric.ManualReader, *safeBuffer) {
	t.Helper()

	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() {
		require.NoError(t, mp.Shutdown(context.Background()))
	})

	logs := &safeBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	srv, err := newRollDiceServer(noop.NewTracerProvider(), mp, roll, logger)
	require.NoError(t, err)

	return srv, reader, logs
}

// collect gathers the current metrics from the reader.
func collect(t *testing.T, reader *metric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	return rm
}

// findMetric returns the metric with the given name, or fails the test.
func findMetric(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m
			}
		}
	}
	require.Failf(t, "metric not found", "metric %q not found in collected metrics", name)
	return metricdata.Metrics{}
}

func TestRollDiceSuccess(t *testing.T) {
	t.Parallel()

	srv, reader, logs := newTestServer(t, func() (int, error) { return 4, nil })

	req := httptest.NewRequest(http.MethodGet, "/rolldice", nil)
	rec := httptest.NewRecorder()

	srv.rolldice(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "4\n", rec.Body.String())

	// The handler logged the roll via the injected logger.
	logOutput := logs.String()
	assert.Contains(t, logOutput, "Rolled a dice")
	assert.Contains(t, logOutput, "result=4")

	rm := collect(t, reader)

	// Success counter incremented exactly once.
	successes := findMetric(t, rm, "dice.roll.successes")
	sum, ok := successes.Data.(metricdata.Sum[int64])
	require.Truef(t, ok, "dice.roll.successes is %T, want metricdata.Sum[int64]", successes.Data)
	require.Len(t, sum.DataPoints, 1)
	assert.Equal(t, int64(1), sum.DataPoints[0].Value)

	// Histogram recorded the roll result once with the rolled value.
	hist := findMetric(t, rm, "dice.roll.result")
	h, ok := hist.Data.(metricdata.Histogram[int64])
	require.Truef(t, ok, "dice.roll.result is %T, want metricdata.Histogram[int64]", hist.Data)
	require.Len(t, h.DataPoints, 1)
	assert.Equal(t, uint64(1), h.DataPoints[0].Count)
	assert.Equal(t, int64(4), h.DataPoints[0].Sum)
}

func TestRollDiceError(t *testing.T) {
	t.Parallel()

	srv, reader, logs := newTestServer(t, func() (int, error) {
		return 0, errSevenNotRealistic
	})

	req := httptest.NewRequest(http.MethodGet, "/rolldice", nil)
	rec := httptest.NewRecorder()

	srv.rolldice(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), errSevenNotRealistic.Error())

	// The handler logged the error via the injected logger.
	logOutput := logs.String()
	assert.Contains(t, logOutput, "rollDice Error")
	assert.Contains(t, logOutput, errSevenNotRealistic.Error())

	// On the error path no success metrics are recorded.
	rm := collect(t, reader)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "dice.roll.successes" {
				if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
					assert.Emptyf(t, sum.DataPoints, "dice.roll.successes should record no data points on error path")
				}
			}
			if m.Name == "dice.roll.result" {
				if h, ok := m.Data.(metricdata.Histogram[int64]); ok {
					assert.Emptyf(t, h.DataPoints, "dice.roll.result should record no data points on error path")
				}
			}
		}
	}
}

func TestDefaultRollInRange(t *testing.T) {
	t.Parallel()

	// defaultRoll either returns 1..6 or the seven error. defaultRoll busy-waits
	// ~1s per call, so keep iterations low.
	for i := 0; i < 2; i++ {
		got, err := defaultRoll()
		if err != nil {
			continue
		}
		assert.GreaterOrEqual(t, got, 1)
		assert.LessOrEqual(t, got, 6)
	}
}
