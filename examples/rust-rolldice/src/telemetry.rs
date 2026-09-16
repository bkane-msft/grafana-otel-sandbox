use anyhow::{Result, anyhow};
use opentelemetry::{
    propagation::{TextMapCompositePropagator, TextMapPropagator},
    trace::TracerProvider as _,
};
use opentelemetry_appender_tracing::layer::OpenTelemetryTracingBridge;
use opentelemetry_otlp::{LogExporter, MetricExporter, SpanExporter};
use opentelemetry_sdk::{
    Resource,
    logs::SdkLoggerProvider,
    metrics::SdkMeterProvider,
    propagation::{BaggagePropagator, TraceContextPropagator},
    trace::SdkTracerProvider,
};
use tracing_subscriber::{
    Layer as _, Registry,
    filter::{LevelFilter, Targets},
    layer::SubscriberExt as _,
    util::SubscriberInitExt as _,
};

use crate::INSTRUMENTATION_SCOPE;

pub struct Telemetry {
    pub meter_provider: SdkMeterProvider,
    tracer_provider: SdkTracerProvider,
    logger_provider: SdkLoggerProvider,
}

/// Which exporter a signal uses, selected by an `OTEL_<SIGNAL>_EXPORTER` env
/// var. Matches the subset of Go's autoexport we care about.
#[derive(Clone, Copy)]
enum ExporterKind {
    Otlp,
    Console,
    None,
}

impl ExporterKind {
    fn from_env(var: &str) -> Result<Self> {
        match std::env::var(var)
            .unwrap_or_default()
            .to_ascii_lowercase()
            .as_str()
        {
            "" | "otlp" => Ok(Self::Otlp),
            "console" | "stdout" => Ok(Self::Console),
            "none" => Ok(Self::None),
            other => Err(anyhow!(
                "unsupported {var}={other:?}; expected otlp, console, or none"
            )),
        }
    }
}

/// Builds the text-map propagator from `OTEL_PROPAGATORS` (default
/// `tracecontext,baggage`), mirroring Go's `autoprop.NewTextMapPropagator`.
/// Returns `None` for `none` (no propagation), so the caller leaves the global
/// no-op propagator untouched. The propagator is returned rather than installed
/// so the global side effect lives at the composition root (`main`), keeping
/// library and test code free of process-wide state.
pub fn propagator_from_env() -> Result<Option<TextMapCompositePropagator>> {
    build_propagator(&std::env::var("OTEL_PROPAGATORS").unwrap_or_default())
}

/// Builds the composite propagator named by `value` (comma-separated, e.g.
/// `"tracecontext,baggage"`). Empty defaults to `tracecontext,baggage`. `none`
/// yields `None`, meaning "leave the global no-op propagator in place". Only the
/// W3C `tracecontext` and `baggage` propagators are supported; any other entry
/// is rejected.
fn build_propagator(value: &str) -> Result<Option<TextMapCompositePropagator>> {
    let value = value.trim();
    let names = if value.is_empty() {
        "tracecontext,baggage"
    } else {
        value
    };

    let entries = names
        .split(',')
        .map(str::trim)
        .filter(|entry| !entry.is_empty())
        .collect::<Vec<_>>();

    if entries
        .iter()
        .any(|entry| entry.eq_ignore_ascii_case("none"))
    {
        return Ok(None);
    }

    let mut propagators: Vec<Box<dyn TextMapPropagator + Send + Sync>> = Vec::new();
    for entry in entries {
        match entry.to_ascii_lowercase().as_str() {
            "tracecontext" => propagators.push(Box::new(TraceContextPropagator::new())),
            "baggage" => propagators.push(Box::new(BaggagePropagator::new())),
            other => {
                return Err(anyhow!(
                    "unsupported OTEL_PROPAGATORS entry {other:?}; expected tracecontext, baggage, or none"
                ));
            }
        }
    }

    Ok(Some(TextMapCompositePropagator::new(propagators)))
}

impl Telemetry {
    pub fn init() -> Result<Self> {
        let resource = Resource::builder().build();

        // Each signal's destination is chosen by OTEL_<SIGNAL>_EXPORTER,
        // mirroring the Go example's autoexport. OTLP endpoint/protocol and the
        // metric interval are read from their standard env vars by the SDK.
        let mut tracer_provider = SdkTracerProvider::builder().with_resource(resource.clone());
        tracer_provider = match ExporterKind::from_env("OTEL_TRACES_EXPORTER")? {
            ExporterKind::Otlp => {
                tracer_provider.with_batch_exporter(SpanExporter::builder().build()?)
            }
            ExporterKind::Console => {
                tracer_provider.with_simple_exporter(opentelemetry_stdout::SpanExporter::default())
            }
            ExporterKind::None => tracer_provider,
        };
        let tracer_provider = tracer_provider.build();

        let mut meter_provider = SdkMeterProvider::builder().with_resource(resource.clone());
        meter_provider = match ExporterKind::from_env("OTEL_METRICS_EXPORTER")? {
            ExporterKind::Otlp => {
                meter_provider.with_periodic_exporter(MetricExporter::builder().build()?)
            }
            ExporterKind::Console => meter_provider
                .with_periodic_exporter(opentelemetry_stdout::MetricExporter::default()),
            ExporterKind::None => meter_provider,
        };
        let meter_provider = meter_provider.build();

        let mut logger_provider = SdkLoggerProvider::builder().with_resource(resource);
        logger_provider = match ExporterKind::from_env("OTEL_LOGS_EXPORTER")? {
            ExporterKind::Otlp => {
                logger_provider.with_batch_exporter(LogExporter::builder().build()?)
            }
            ExporterKind::Console => {
                logger_provider.with_simple_exporter(opentelemetry_stdout::LogExporter::default())
            }
            ExporterKind::None => logger_provider,
        };
        let logger_provider = logger_provider.build();

        let filter = std::env::var("RUST_LOG")
            .or_else(|_| std::env::var("LOG_LEVEL"))
            .unwrap_or_else(|_| "info".to_owned());
        let exporter_filter = Targets::new()
            .with_default(LevelFilter::INFO)
            .with_target("opentelemetry", LevelFilter::OFF)
            .with_target("opentelemetry_sdk", LevelFilter::OFF)
            .with_target("opentelemetry_otlp", LevelFilter::OFF)
            .with_target("hyper", LevelFilter::OFF)
            .with_target("h2", LevelFilter::OFF)
            .with_target("reqwest", LevelFilter::OFF)
            .with_target("tonic", LevelFilter::OFF);

        let subscriber = Registry::default()
            .with(tracing_subscriber::EnvFilter::new(filter))
            .with(tracing_subscriber::fmt::layer())
            .with(
                tracing_opentelemetry::layer()
                    .with_tracer(tracer_provider.tracer(INSTRUMENTATION_SCOPE))
                    .with_filter(exporter_filter.clone()),
            )
            .with(OpenTelemetryTracingBridge::new(&logger_provider).with_filter(exporter_filter));
        // The one telemetry global installed here rather than in `main`:
        // registering the default subscriber is the unavoidable "make logging
        // work at all" step (analogous to Go's slog.SetDefault), and returning
        // the composed subscriber just to install it in `main` adds type noise
        // for no real decoupling. All other globals (the propagator) live in
        // `main`; tests use TestTelemetry's scoped Dispatch and install neither.
        subscriber.try_init()?;

        Ok(Self {
            meter_provider,
            tracer_provider,
            logger_provider,
        })
    }

    pub fn shutdown(self) -> Result<()> {
        let errors = [
            ("traces", self.tracer_provider.shutdown()),
            ("metrics", self.meter_provider.shutdown()),
            ("logs", self.logger_provider.shutdown()),
        ]
        .into_iter()
        .filter_map(|(signal, result)| result.err().map(|error| format!("{signal}: {error}")))
        .collect::<Vec<_>>();

        if errors.is_empty() {
            Ok(())
        } else {
            Err(anyhow!("telemetry shutdown failed: {}", errors.join("; ")))
        }
    }
}

#[cfg(test)]
pub struct TestTelemetry {
    pub dispatch: tracing::Dispatch,
    pub meter_provider: SdkMeterProvider,
    metric_exporter: opentelemetry_sdk::metrics::InMemoryMetricExporter,
    span_exporter: opentelemetry_sdk::trace::InMemorySpanExporter,
    log_exporter: opentelemetry_sdk::logs::InMemoryLogExporter,
    tracer_provider: SdkTracerProvider,
    logger_provider: SdkLoggerProvider,
}

#[cfg(test)]
impl TestTelemetry {
    pub fn new() -> Self {
        use opentelemetry_sdk::{
            logs::InMemoryLogExporter,
            metrics::{InMemoryMetricExporter, PeriodicReader},
            trace::InMemorySpanExporter,
        };

        let metric_exporter = InMemoryMetricExporter::default();
        let span_exporter = InMemorySpanExporter::default();
        let log_exporter = InMemoryLogExporter::default();
        let tracer_provider = SdkTracerProvider::builder()
            .with_simple_exporter(span_exporter.clone())
            .build();
        let meter_provider = SdkMeterProvider::builder()
            .with_reader(PeriodicReader::builder(metric_exporter.clone()).build())
            .build();
        let logger_provider = SdkLoggerProvider::builder()
            .with_simple_exporter(log_exporter.clone())
            .build();

        let subscriber = Registry::default()
            .with(
                tracing_opentelemetry::layer()
                    .with_tracer(tracer_provider.tracer(INSTRUMENTATION_SCOPE)),
            )
            .with(OpenTelemetryTracingBridge::new(&logger_provider));

        Self {
            dispatch: tracing::Dispatch::new(subscriber),
            meter_provider,
            metric_exporter,
            span_exporter,
            log_exporter,
            tracer_provider,
            logger_provider,
        }
    }

    pub fn flush(&self) {
        self.tracer_provider.force_flush().unwrap();
        self.meter_provider.force_flush().unwrap();
        self.logger_provider.force_flush().unwrap();
    }

    pub fn metrics(&self) -> Vec<opentelemetry_sdk::metrics::data::ResourceMetrics> {
        self.metric_exporter.get_finished_metrics().unwrap()
    }

    pub fn spans(&self) -> Vec<opentelemetry_sdk::trace::SpanData> {
        self.span_exporter.get_finished_spans().unwrap()
    }

    pub fn logs_contain(&self, expected: &str) -> bool {
        use opentelemetry::logs::AnyValue;

        self.log_exporter
            .get_emitted_logs()
            .unwrap()
            .iter()
            .any(|log| {
                matches!(
                    log.record.body(),
                    Some(AnyValue::String(value)) if value.as_str().contains(expected)
                )
            })
    }

    pub fn has_error_log(&self) -> bool {
        self.log_exporter
            .get_emitted_logs()
            .unwrap()
            .iter()
            .any(|log| log.record.severity_text() == Some("ERROR"))
    }

    pub fn has_correlated_log(&self) -> bool {
        self.log_exporter
            .get_emitted_logs()
            .unwrap()
            .iter()
            .any(|log| log.record.trace_context().is_some())
    }
}

#[cfg(test)]
impl Drop for TestTelemetry {
    fn drop(&mut self) {
        let _ = self.tracer_provider.shutdown();
        let _ = self.meter_provider.shutdown();
        let _ = self.logger_provider.shutdown();
    }
}

#[cfg(test)]
mod tests {
    use super::build_propagator;

    #[test]
    fn empty_defaults_to_w3c_composite() {
        assert!(build_propagator("").unwrap().is_some());
        assert!(build_propagator("   ").unwrap().is_some());
    }

    #[test]
    fn accepts_supported_names_case_and_space_insensitively() {
        assert!(build_propagator("tracecontext").unwrap().is_some());
        assert!(build_propagator("baggage").unwrap().is_some());
        assert!(
            build_propagator(" TraceContext , Baggage ")
                .unwrap()
                .is_some()
        );
    }

    #[test]
    fn none_yields_no_propagator() {
        assert!(build_propagator("none").unwrap().is_none());
        // `none` anywhere in the list disables propagation entirely.
        assert!(build_propagator("tracecontext,none").unwrap().is_none());
    }

    #[test]
    fn unsupported_name_is_rejected() {
        let error = build_propagator("bogus").unwrap_err().to_string();
        assert!(error.contains("bogus"), "unexpected error: {error}");
        // A single bad entry fails the whole list.
        assert!(build_propagator("tracecontext,bogus").is_err());
    }
}
