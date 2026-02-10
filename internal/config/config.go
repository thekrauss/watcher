package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Config holds validated runtime settings.
type Config struct {
	RestateURL         string        `mapstructure:"restate_url"`
	ClusterID          string        `mapstructure:"cluster_id"`
	WatchNamespaceMode string        `mapstructure:"watch_namespace_mode"`
	WatchNamespace     string        `mapstructure:"watch_namespace"`
	ManagedNSLabel     string        `mapstructure:"managed_ns_label"`
	HTTPListen         string        `mapstructure:"http_listen"`
	RestateCallTimeout time.Duration `mapstructure:"restate_timeout"`
	RemoteConfigURL    string        `mapstructure:"remote_config_url"`
	RemoteConfigFetch  time.Duration `mapstructure:"remote_config_fetch"`
	BatchSize          int           `mapstructure:"batch_size"`
}

const (
	defaultWatchNamespaceMode = "single"
	defaultHTTPListen         = ":8080"
	defaultRestateTimeout     = 5 * time.Second
	defaultRemoteConfigFetch  = 5 * time.Minute
)

// Load parses flags, environment variables and config file using Viper.
func Load() (Config, error) {
	// Define flags using pflag for better viper integration
	pflag.String("restate-url", "", "Base URL for Restate service")
	pflag.String("cluster-id", "", "Cluster identifier")
	pflag.String("watch-namespace-mode", defaultWatchNamespaceMode, "Namespace selection mode: single|label")
	pflag.String("watch-namespace", "", "Target namespace when mode=single")
	pflag.String("managed-ns-label", "", "Namespace label selector when mode=label")
	pflag.String("http-listen", defaultHTTPListen, "HTTP listen address for health endpoints")
	pflag.Duration("restate-timeout", defaultRestateTimeout, "Timeout for Restate calls")
	pflag.String("remote-config-url", "", "URL to fetch remote configuration from")
	pflag.Duration("remote-config-fetch", defaultRemoteConfigFetch, "Interval to fetch remote configuration")
	pflag.Int("batch-size", 100, "Batch size for processing")
	pflag.Parse()

	// Bind flags to viper
	if err := viper.BindPFlags(pflag.CommandLine); err != nil {
		return Config{}, fmt.Errorf("bind flags: %w", err)
	}

	// Environment variables
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	viper.AutomaticEnv()

	// Config file
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	if err := viper.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFoundError) {
			return Config{}, fmt.Errorf("read config file: %w", err)
		}
		// Config file not found is okay, we might use env/flags
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validate(cfg Config) error {
	if cfg.RestateURL == "" {
		return errors.New("restate_url is required")
	}
	if cfg.ClusterID == "" {
		return errors.New("cluster_id is required")
	}
	switch cfg.WatchNamespaceMode {
	case "single":
		if cfg.WatchNamespace == "" {
			return errors.New("watch_namespace is required when watch_namespace_mode=single")
		}
	case "label":
		if cfg.ManagedNSLabel == "" {
			return errors.New("managed_ns_label is required when watch_namespace_mode=label")
		}
	default:
		return fmt.Errorf("unsupported watch_namespace_mode: %s", cfg.WatchNamespaceMode)
	}
	return nil
}
