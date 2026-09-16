use std::{
    hint,
    sync::Arc,
    time::{Duration, Instant},
};

use anyhow::{Result, anyhow, bail};
use axum::{
    Router,
    body::Body,
    extract::{MatchedPath, Request, State},
    http::{Response, StatusCode},
    response::IntoResponse,
    routing::get,
};
use opentelemetry::{
    KeyValue,
    metrics::{Counter, Histogram, Meter},
};
use rand::RngExt as _;
use tower_http::trace::TraceLayer;
use tracing::{Span, field};

pub type Roller = dyn Fn() -> Result<u64> + Send + Sync;

#[derive(Clone)]
pub struct AppState {
    roller: Arc<Roller>,
    roll_result: Histogram<u64>,
    roll_successes: Counter<u64>,
}

impl AppState {
    pub fn new(roller: Arc<Roller>, meter: &Meter) -> Self {
        Self {
            roller,
            roll_result: meter
                .u64_histogram("dice.roll.result")
                .with_description("The result of a successful dice roll")
                .with_unit("{roll}")
                .with_boundaries(vec![1., 2., 3., 4., 5., 6.])
                .build(),
            roll_successes: meter
                .u64_counter("dice.roll.successes")
                .with_description("The number of successful dice rolls")
                .with_unit("{rolls}")
                .build(),
        }
    }
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/rolldice", get(rolldice))
        .layer(
            TraceLayer::new_for_http()
                .make_span_with(|request: &Request| {
                    let route = request
                        .extensions()
                        .get::<MatchedPath>()
                        .map_or_else(|| request.uri().path(), MatchedPath::as_str);

                    tracing::info_span!(
                        "http.request",
                        otel.name = %format_args!("{} {route}", request.method()),
                        otel.kind = "server",
                        http.request.method = %request.method(),
                        http.route = route,
                        http.response.status_code = field::Empty,
                        otel.status_code = field::Empty,
                    )
                })
                .on_response(
                    |response: &Response<Body>, latency: Duration, span: &Span| {
                        span.record("http.response.status_code", response.status().as_u16());
                        if response.status().is_server_error() {
                            span.record("otel.status_code", "error");
                        }
                        tracing::info!(
                            parent: span,
                            status = response.status().as_u16(),
                            latency_ms = latency.as_millis(),
                            "Request completed"
                        );
                    },
                ),
        )
        .with_state(state)
}

#[tracing::instrument(
    name = "roll",
    skip(state),
    fields(
        result = field::Empty,
        otel.status_code = field::Empty,
        otel.status_description = field::Empty
    )
)]
async fn rolldice(State(state): State<AppState>) -> impl IntoResponse {
    let roller = Arc::clone(&state.roller);
    let result = tokio::task::spawn_blocking(move || roller())
        .await
        .map_err(|error| anyhow!("roller task failed: {error}"))
        .and_then(|result| result);

    match result {
        Ok(roll) => {
            Span::current().record("result", roll);
            tracing::info!(result = roll, "Rolled a dice");

            state.roll_result.record(roll, &[]);
            state
                .roll_successes
                .add(1, &[KeyValue::new("result", roll as i64)]);

            (StatusCode::OK, format!("{roll}\n"))
        }
        Err(error) => {
            Span::current().record("otel.status_code", "error");
            Span::current().record("otel.status_description", error.to_string());
            tracing::error!(error = %error, "rollDice Error");
            (StatusCode::INTERNAL_SERVER_ERROR, format!("{error}\n"))
        }
    }
}

pub fn default_roll() -> Result<u64> {
    let start = Instant::now();
    while start.elapsed() < Duration::from_secs(1) {
        hint::spin_loop();
    }

    let roll = rand::rng().random_range(1..=7);
    if roll == 7 {
        bail!("dice rolled a seven, which is not realistic");
    }
    Ok(roll)
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use axum::{
        body::Body,
        http::{Request, StatusCode},
    };
    use http_body_util::BodyExt as _;
    use opentelemetry::{KeyValue, metrics::MeterProvider as _, trace::Status};
    use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData, ResourceMetrics};
    use tower::ServiceExt as _;
    use tracing::instrument::WithSubscriber as _;

    use super::{AppState, router};
    use crate::{INSTRUMENTATION_SCOPE, telemetry::TestTelemetry};

    #[tokio::test]
    async fn success_records_response_logs_trace_and_metrics() {
        let telemetry = TestTelemetry::new();
        let meter = telemetry.meter_provider.meter(INSTRUMENTATION_SCOPE);
        let app = router(AppState::new(Arc::new(|| Ok(4)), &meter));

        let response = app
            .oneshot(
                Request::builder()
                    .uri("/rolldice")
                    .body(Body::empty())
                    .unwrap(),
            )
            .with_subscriber(telemetry.dispatch.clone())
            .await
            .unwrap();

        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.into_body().collect().await.unwrap().to_bytes(),
            "4\n"
        );

        telemetry.flush();
        assert!(telemetry.logs_contain("Rolled a dice"));
        assert!(telemetry.has_correlated_log());
        assert!(telemetry.spans().iter().any(|span| span.name == "roll"));

        let metrics = telemetry.metrics();
        let successes = find_metric(&metrics, "dice.roll.successes");
        let AggregatedMetrics::U64(MetricData::Sum(successes)) = successes.data() else {
            panic!("dice.roll.successes was not a u64 sum");
        };
        let success = successes.data_points().next().unwrap();
        assert_eq!(success.value(), 1);
        assert!(
            success
                .attributes()
                .any(|attribute| attribute == &KeyValue::new("result", 4_i64))
        );

        let histogram = find_metric(&metrics, "dice.roll.result");
        let AggregatedMetrics::U64(MetricData::Histogram(histogram)) = histogram.data() else {
            panic!("dice.roll.result was not a u64 histogram");
        };
        let histogram = histogram.data_points().next().unwrap();
        assert_eq!(histogram.count(), 1);
        assert_eq!(histogram.sum(), 4);
        assert_eq!(
            histogram.bounds().collect::<Vec<_>>(),
            vec![1., 2., 3., 4., 5., 6.]
        );
        assert_eq!(
            histogram.bucket_counts().collect::<Vec<_>>(),
            vec![0, 0, 0, 1, 0, 0, 0]
        );
    }

    #[tokio::test]
    async fn failure_records_error_log_and_trace_without_metrics() {
        let telemetry = TestTelemetry::new();
        let meter = telemetry.meter_provider.meter(INSTRUMENTATION_SCOPE);
        let app = router(AppState::new(
            Arc::new(|| anyhow::bail!("dice rolled a seven, which is not realistic")),
            &meter,
        ));

        let response = app
            .oneshot(
                Request::builder()
                    .uri("/rolldice")
                    .body(Body::empty())
                    .unwrap(),
            )
            .with_subscriber(telemetry.dispatch.clone())
            .await
            .unwrap();

        assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
        let body = response.into_body().collect().await.unwrap().to_bytes();
        assert!(
            String::from_utf8_lossy(&body).contains("dice rolled a seven, which is not realistic")
        );

        telemetry.flush();
        assert!(telemetry.logs_contain("rollDice Error"));
        assert!(telemetry.has_error_log());
        assert!(
            telemetry
                .spans()
                .iter()
                .any(|span| span.name == "roll" && matches!(span.status, Status::Error { .. }))
        );

        let metrics = telemetry.metrics();
        assert!(maybe_find_metric(&metrics, "dice.roll.successes").is_none());
        assert!(maybe_find_metric(&metrics, "dice.roll.result").is_none());
    }

    fn find_metric<'a>(
        metrics: &'a [ResourceMetrics],
        name: &str,
    ) -> &'a opentelemetry_sdk::metrics::data::Metric {
        maybe_find_metric(metrics, name).unwrap_or_else(|| panic!("metric {name:?} not found"))
    }

    fn maybe_find_metric<'a>(
        metrics: &'a [ResourceMetrics],
        name: &str,
    ) -> Option<&'a opentelemetry_sdk::metrics::data::Metric> {
        metrics
            .iter()
            .flat_map(ResourceMetrics::scope_metrics)
            .flat_map(|scope| scope.metrics())
            .find(|metric| metric.name() == name)
    }
}
