# Config Package

The `config` package handles the application configuration using [Viper](https://github.com/spf13/viper). It provides a unified way to manage settings via:
1. **Command-line flags** (highest priority)
2. **Environment variables**
3. **YAML configuration file** (default: `config.yaml` in the root or `./config/config.yaml`)

## Configuration Structure

| Key | Flag | Env Var | Description |
|-----|------|---------|-------------|
| `restate_url` | `--restate-url` | `RESTATE_URL` | Base URL for the Restate service (Required) |
| `cluster_id` | `--cluster-id` | `CLUSTER_ID` | Unique identifier for this cluster (Required) |
| `watch_namespace_mode` | `--watch-namespace-mode` | `WATCH_NAMESPACE_MODE` | `single` or `label` mode (Default: `single`) |
| `watch_namespace` | `--watch-namespace` | `WATCH_NAMESPACE` | Target namespace when in `single` mode |
| `managed_ns_label` | `--managed-ns-label` | `MANAGED_NS_LABEL` | Label selector when in `label` mode |
| `http_listen` | `--http-listen` | `HTTP_LISTEN` | Address for health/metrics endpoints (Default: `:8080`) |
| `restate_timeout` | `--restate-timeout` | `RESTATE_TIMEOUT` | Timeout for Restate API calls (Default: `5s`) |
| `remote_config_url` | `--remote-config-url` | `REMOTE_CONFIG_URL` | URL to fetch fleet configuration |
| `remote_config_fetch`| `--remote-config-fetch`| `REMOTE_CONFIG_FETCH`| Interval for remote sync (Default: `5m`) |
| `batch_size` | `--batch-size` | `BATCH_SIZE` | Batch size for processing (Default: `100`) |

## Usage

```go
cfg, err := config.Load()
if err != nil {
    log.Fatal(err)
}
```

## Validation

The `validate()` function ensures that all required parameters are set based on the operation mode. If a required field is missing (like `restate_url` or `cluster_id`), the application will fail to start with a descriptive error.
