# Metrics Package

The `metrics` package defines the Prometheus metrics used to monitor the health and performance of the watcher.

## Available Metrics

### Kubernetes Events
- `events_total`: Counter tracking all pod events received from the informers. Labels: `type`, `reason`, `namespace`.
- `events_deduped_total`: Counter for events that were suppressed by the deduplication engine.

### System Performance
- `queue_depth`: Gauge showing the number of events currently buffered in the `EventProcessor` awaiting flush.
- `reconnects_total`: Counter showing how many times Kubernetes informers have been initialized.

### External Integration
- `restate_requests_total`: Counter for all outgoing HTTP calls to the Restate backend. Labels: `code` (HTTP status).

## Usage

Metrics are registered globally and can be incremented from anywhere in the codebase:

```go
import "github.com/thekrauss/watcher/internal/metrics"

metrics.EventsTotal.WithLabelValues("Modified", "OOMKilled", "prod").Inc()
```

The metrics are exposed on the HTTP server at `/metrics` (configured in `cmd/server/main.go`).
