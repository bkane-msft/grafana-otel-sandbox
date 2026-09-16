mod app;
mod telemetry;

use std::sync::Arc;

use anyhow::Result;
use opentelemetry::metrics::MeterProvider as _;
use tokio::net::TcpListener;

use crate::app::{AppState, default_roll, router};
use crate::telemetry::Telemetry;

const ADDRESS: &str = "0.0.0.0:8081";
const INSTRUMENTATION_SCOPE: &str = "rust-rolldice";

#[tokio::main]
async fn main() -> Result<()> {
    let telemetry = Telemetry::init()?;

    // Install the global text-map propagator (W3C tracecontext + baggage by
    // default, via OTEL_PROPAGATORS) so inbound requests continue upstream
    // traces. Installed here at the composition root — like Go's main.go calling
    // otel.SetTextMapPropagator — so library and test code never touch globals.
    if let Some(propagator) = telemetry::propagator_from_env()? {
        opentelemetry::global::set_text_map_propagator(propagator);
    }

    let meter = telemetry.meter_provider.meter(INSTRUMENTATION_SCOPE);
    let app = router(AppState::new(Arc::new(default_roll), &meter));
    let listener = TcpListener::bind(ADDRESS).await?;

    tracing::info!(address = ADDRESS, "Listening");
    axum::serve(listener, app)
        .with_graceful_shutdown(shutdown_signal())
        .await?;

    telemetry.shutdown()
}

async fn shutdown_signal() {
    let _ = tokio::signal::ctrl_c().await;
}
