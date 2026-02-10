# Restate Package

The `restate` package provides the client and data structures for communicating with the Restate backend (the "manager").

## Client Interface

The package defines a `IRestateClient` interface to allow for easy mocking and testing of the backend integration:

```go
type IRestateClient interface {
    Enroll(ctx context.Context) error
    GetRemoteConfig(ctx context.Context) (*RemoteConfig, error)
    NotifyPodEvent(ctx context.Context, event PodEvent) error
    NotifyPodEvents(ctx context.Context, events []PodEvent) error
}
```

## RestateClient

The `RestateClient` is the default implementation of the `IRestateClient` interface. It is a resilient HTTP client designed for high-availability event delivery.

### Resilience Features

- **Circuit Breaker**: Uses `github.com/sony/gobreaker` to prevent cascading failures. If the backend is down, the client will "trip" and stop making requests until the health is restored.
- **Exponential Backoff**: Implements a retry strategy for 500-series errors and network failures.
- **Timeouts**: Context-aware requests with configurable timeouts.

### APIs Implemented

- `Enroll(ctx)`: Registers the cluster with its `cluster_id` and version.
- `GetRemoteConfig(ctx)`: Fetches dynamic processing parameters from the management layer.
- `NotifyPodEvent(ctx, event)`: Sends a single pod event to the Restate service.
- `NotifyPodEvents(ctx, events)`: Sends a batch of pod events to the Restate service.

## Data Models

### PodEvent
The canonical format for pod status updates. It includes:
- Metadata: `namespace`, `name`, `uid`.
- State: `phase`, `reason`, `message`, `restart_count`.
- Sharding/Tenancy: `labels`, `owner_id`, `project_id` (enriched via labels or owner references in the future).

### RemoteConfig
Configuration received from the fleet manager to tune the watcher behavior:
- `DedupCooldown`: Time window for deduplication.
- `BatchSize`: Maximum events per HTTP request.
- `BatchFlush`: Maximum time an event stays in the local buffer.
- `EnabledReasons`: List of Kubernetes reasons (e.g., `Evicted`) to capture.

## Integration

All requests are tracked via Prometheus metrics (`internal/metrics`):
- `restate_requests_total`: Iterated by status code to monitor backend health and latency.
