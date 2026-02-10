# Kubernetes Package

The `kubernetes` package is the engine of the watcher. It is responsible for observing Kubernetes resources (specifically Pods) and processing events before sending them to the backend.

## Interfaces

The package provides two main interfaces to decouple orchestration from implementation:

### 1. Watcher (`watcher.go`)
Defines the lifecycle of the Kubernetes resource manager.
```go
type Watcher interface {
    Run(ctx context.Context) error
}
```

### 2. Processor (`processor.go`)
Defines the logic for handling captured events.
```go
type Processor interface {
    ApplyRemoteConfig(cfg *restate.RemoteConfig)
    Process(ctx context.Context, event restate.PodEvent)
    Run(ctx context.Context)
    CleanupLoop(ctx context.Context)
    GetMetadata(podName string) (projectID, workloadID string, ok bool)
}
```

## Components

### 1. Manager (`watcher.go`)
The `Manager` is the default implementation of `Watcher`. It orchestrates the lifecycle of Kubernetes watchers.
- **Dynamic Namespace Discovery**: When `watch_namespace_mode` is set to `label`, it uses a Shared Informer to monitor namespace events and dynamically starts/stops watchers for namespaces matching the specified label.
- **Shared Informers**: Uses `client-go` informers for efficient, event-driven access to Kubernetes resources.
- **Config Sync**: Runs a background loop to fetch remote configuration from Restate and apply it to the processor at runtime.
- **Cluster Enrollment**: Automatically registers the cluster with the Restate backend on startup.

### 2. EventProcessor (`processor.go`)
The `EventProcessor` handles all event logic post-capture.
- **Filtering**: Only processes specific pod failure reasons (e.g., `CrashLoopBackOff`, `OOMKilled`).
- **Deduplication**: Uses a `sync.Map` to prevent flooding the backend with repetitive events for the same container.
- **Batch Processing**: Buffers events and flushes them in batches to the backend to reduce network overhead and improve throughput.
- **Metadata Caching**: Extracts and stores `project-id` and `workload-id` from pod labels into a high-performance concurrent cache with automatic TTL-based cleanup.
- **Audit Logging**: Generates high-fidelity JSON logs for every event processed, including a SHA-256 payload hash for traceability.
- **Thread Safety**: Uses `sync.RWMutex` to allow safe dynamic configuration updates (like changing batch sizes or cooldowns) without restarting the service.

## Key Methods

- `Run(ctx)`: Starts the main orchestration loops (processor, cleanup, config sync).
- `handlePodEvent(...)`: Maps raw Kubernetes objects into structured `PodEvent` payloads.
- `ApplyRemoteConfig(cfg)`: Hot-swaps processing parameters at runtime.
- `GetMetadata(podName)`: Retrieves cached project and workload identifiers for telemetry enrichment.

## Metrics

Exposes Prometheus metrics via the `internal/metrics` package:
- `events_total`: Total events seen by type and reason.
- `events_deduped_total`: Count of events suppressed by the deduplication logic.
- `queue_depth`: Current number of events waiting in the batching queue.
- `reconnects_total`: Number of times informers have been (re)started.
