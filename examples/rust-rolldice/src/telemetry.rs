use anyhow::Result;
use opentelemetry::trace::TracerProvider as _;
use opentelemetry_appender_tracing::layer::OpenTelemetryTracingBridge;
use opentelemetry_otlp::{LogExporter, MetricExporter, SpanExporter};
use opentelemetry_sdk::{
    Resource, logs::SdkLoggerProvider, metrics::SdkMeterProvider, trace::SdkTracerProvider,
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

impl Telemetry {
    pub fn init() -> Result<Self> {
        let resource = Resource::builder().build();
        let tracer_provider = SdkTracerProvider::builder()
            .with_batch_exporter(SpanExporter::builder().build()?)
            .with_resource(resource.clone())
            .build();
        let meter_provider = SdkMeterProvider::builder()
            .with_periodic_exporter(MetricExporter::builder().build()?)
            .with_resource(resource.clone())
            .build();
        let logger_provider = SdkLoggerProvider::builder()
            .with_batch_exporter(LogExporter::builder().build()?)
            .with_resource(resource)
            .build();

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
                    .with_tracer(tracer_provider.tracer(INSTRUMENTATION_SCOPE)),
            )
            .with(OpenTelemetryTracingBridge::new(&logger_provider).with_filter(exporter_filter));
        subscriber.try_init()?;

        Ok(Self {
            meter_provider,
            tracer_provider,
            logger_provider,
        })
    }

    pub fn shutdown(self) -> Result<()> {
        self.tracer_provider.shutdown()?;
        self.meter_provider.shutdown()?;
        self.logger_provider.shutdown()?;
        Ok(())
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
