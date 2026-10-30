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

	"github.com/thekrauss/watcher/internal/auth"
	"github.com/thekrauss/watcher/internal/metrics"
)

// Client defines the interface for communicating with the Restate backend.
type IRestateClient interface {
	Enroll(ctx context.Context) error
	GetRemoteConfig(ctx context.Context) (*RemoteConfig, error)
	NotifyPodEvent(ctx context.Context, event PodEvent) error
	// ReportServiceAccountKeys : publie l'émetteur et le JWKS des jetons de
	// comptes Kubernetes du cluster (identité des workloads betorun).
	ReportServiceAccountKeys(ctx context.Context, issuer string, jwks json.RawMessage) error
}

type RestateClient struct {
	url       string
	clusterID string
	auth      auth.Authenticator // fichier de clé (jeton STS) ou clé API
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

// NewRestateClient construit un client Restate-compatible ; a pose
// l'authentification de chaque requête (auth.None en dev local).
func NewRestateClient(baseURL, clusterID string, a auth.Authenticator, timeout time.Duration, log *zap.SugaredLogger) *RestateClient {
	if a == nil {
		a = auth.None{}
	}
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
		auth:      a,
		http: &http.Client{
			Timeout: timeout,
		},
		log: log,
		cb:  cb,
	}
}

// errUnauthorized : identifiants refusés ; l'authentification est
// renouvelée avant le prochain essai (jeton expiré ou clé tournée).
var errUnauthorized = errors.New("unauthorized: check the watcher key")

// checkAuth : sur 401, invalide le jeton pour que l'essai suivant en obtienne un neuf.
func (c *RestateClient) checkAuth(resp *http.Response) error {
	if resp.StatusCode == http.StatusUnauthorized {
		c.auth.Invalidate()
		return errUnauthorized
	}
	return nil
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
		if err := c.auth.Apply(ctx, req); err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if err := c.checkAuth(resp); err != nil {
			return nil, err
		}
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
		if err := c.auth.Apply(ctx, req); err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if err := c.checkAuth(resp); err != nil {
			return nil, err
		}

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
			if authErr := c.auth.Apply(ctx, req); authErr != nil {
				return nil, authErr
			}

			resp, doErr := c.http.Do(req)
			if doErr != nil {
				metrics.RestateRequestsTotal.WithLabelValues("error").Inc()
				return nil, doErr
			}
			defer resp.Body.Close()

			metrics.RestateRequestsTotal.WithLabelValues(strconv.Itoa(resp.StatusCode)).Inc()

			if err := c.checkAuth(resp); err != nil {
				return nil, err // réessayé avec un jeton neuf
			}
			if resp.StatusCode == http.StatusForbidden {
				// Clé d'un autre cluster ou rôle retiré : permanent, ne pas insister.
				c.log.Errorw("pod event refused by backend (403): the key does not belong to this cluster",
					"cluster_id", c.clusterID)
				return nil, nil
			}
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

func (c *RestateClient) ReportServiceAccountKeys(ctx context.Context, issuer string, jwks json.RawMessage) error {
	endpoint := fmt.Sprintf("%s/ClusterService/%s/ServiceAccountKeys", c.url, c.clusterID)
	body, err := json.Marshal(map[string]any{"issuer": issuer, "jwks": jwks})
	if err != nil {
		return err
	}
	_, err = c.cb.Execute(func() (interface{}, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if err := c.auth.Apply(ctx, req); err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if err := c.checkAuth(resp); err != nil {
			return nil, err
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("report service account keys failed: %d", resp.StatusCode)
		}
		return nil, nil
	})
	return err
}
