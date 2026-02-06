package restate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/sony/gobreaker"
	"go.uber.org/zap"

	"github.com/thekrauss/watcher/internal/metrics"
)

// Client defines the interface for communicating with the Restate backend.
type IRestateClient interface {
	Enroll(ctx context.Context) error
	GetRemoteConfig(ctx context.Context) (*RemoteConfig, error)
	NotifyPodEvent(ctx context.Context, event PodEvent) error
}

type RestateClient struct {
	url       string
	clusterID string
	http      *http.Client
	log       *zap.SugaredLogger
	cb        *gobreaker.CircuitBreaker
}

type PodEvent struct {
	EventID       string            `json:"event_id"`
	Type          string            `json:"type"`
	Namespace     string            `json:"namespace"`
	Name          string            `json:"name"`
	Phase         string            `json:"phase"`
	Reason        string            `json:"reason,omitempty"`
	Message       string            `json:"message,omitempty"`
	RestartCount  int               `json:"restart_count,omitempty"`
	ContainerName string            `json:"container_name,omitempty"`
	ProjectID     string            `json:"project_id,omitempty"`
	OwnerID       string            `json:"owner_id,omitempty"`
	WorkloadKind  string            `json:"workload_kind,omitempty"`
	WorkloadName  string            `json:"workload_name,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
}

type RemoteConfig struct {
	DedupCooldown  time.Duration
	BatchSize      int
	BatchFlush     time.Duration
	EnabledReasons []string
}

type remoteConfigResponse struct {
	DedupCooldownSeconds int      `json:"dedup_cooldown_seconds"`
	BatchSize            int      `json:"batch_size"`
	BatchFlushMs         int      `json:"batch_flush_ms"`
	EnabledReasons       []string `json:"enabled_reasons"`
}

func NewRestateClient(baseURL, clusterID string, timeout time.Duration, log *zap.SugaredLogger) *RestateClient {
	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "restate-client",
		MaxRequests: 5,
		Interval:    10 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			log.Warnw("Circuit Breaker state changed", "name", name, "from", from.String(), "to", to.String())
		},
	})

	return &RestateClient{
		url:       baseURL,
		clusterID: clusterID,
		http: &http.Client{
			Timeout: timeout,
		},
		log: log,
		cb:  cb,
	}
}

func (c *RestateClient) Enroll(ctx context.Context) error {
	endpoint := fmt.Sprintf("%s/ClusterService/Enroll", c.url)
	body, _ := json.Marshal(map[string]string{
		"cluster_id": c.clusterID,
		"version":    "v1.0.0",
	})

	_, err := c.cb.Execute(func() (interface{}, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("enroll failed: %d", resp.StatusCode)
		}
		return nil, nil
	})
	return err
}

func (c *RestateClient) GetRemoteConfig(ctx context.Context) (*RemoteConfig, error) {
	endpoint := fmt.Sprintf("%s/ConfigService/%s/Get", c.url, c.clusterID)

	result, err := c.cb.Execute(func() (interface{}, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("config fetch failed: %d", resp.StatusCode)
		}

		var cfg remoteConfigResponse
		if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
			return nil, err
		}

		return &RemoteConfig{
			DedupCooldown:  time.Duration(cfg.DedupCooldownSeconds) * time.Second,
			BatchSize:      cfg.BatchSize,
			BatchFlush:     time.Duration(cfg.BatchFlushMs) * time.Millisecond,
			EnabledReasons: cfg.EnabledReasons,
		}, nil
	})

	if err != nil {
		return nil, err
	}
	return result.(*RemoteConfig), nil
}

func (c *RestateClient) NotifyPodEvent(ctx context.Context, event PodEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal pod event: %w", err)
	}

	endpoint := fmt.Sprintf("%s/ProjectService/%s/onPodEvent", c.url, c.clusterID)
	backoff := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}

	for attempt := 0; ; attempt++ {
		_, cbErr := c.cb.Execute(func() (interface{}, error) {
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
			if reqErr != nil {
				return nil, reqErr
			}
			req.Header.Set("Content-Type", "application/json")

			resp, doErr := c.http.Do(req)
			if doErr != nil {
				metrics.RestateRequestsTotal.WithLabelValues("error").Inc()
				return nil, doErr
			}
			defer resp.Body.Close()

			metrics.RestateRequestsTotal.WithLabelValues(strconv.Itoa(resp.StatusCode)).Inc()

			if resp.StatusCode >= http.StatusInternalServerError {
				return nil, fmt.Errorf("restate server error: %d", resp.StatusCode)
			}
			return nil, nil
		})

		if cbErr == nil {
			return nil
		}

		if errors.Is(cbErr, gobreaker.ErrOpenState) {
			return fmt.Errorf("circuit breaker open: %w", cbErr)
		}

		if attempt >= len(backoff) {
			return fmt.Errorf("failed after retries: %w", cbErr)
		}

		select {
		case <-time.After(backoff[attempt]):
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
