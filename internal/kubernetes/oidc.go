package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// Identité des workloads betorun : les pods échangent leur jeton Kubernetes
// projeté auprès du STS betorun, qui doit en vérifier la signature. Le
// watcher lui publie donc l'émetteur et les clés publiques de l'apiserver
// (découverte OIDC des comptes de service), car l'apiserver n'est pas
// joignable depuis l'extérieur. RBAC : ClusterRole
// system:service-account-issuer-discovery.

const (
	keysCheckEvery   = 15 * time.Minute
	keysRepublishAll = time.Hour
)

// serviceAccountKeys : émetteur et JWKS de l'apiserver.
func (m *Manager) serviceAccountKeys(ctx context.Context) (string, json.RawMessage, error) {
	rest := m.clientset.Discovery().RESTClient()
	rawDoc, err := rest.Get().AbsPath(oidcDiscoveryPath).DoRaw(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("service account issuer discovery: %w", err)
	}
	var doc struct {
		Issuer string `json:"issuer"`
	}
	if err := json.Unmarshal(rawDoc, &doc); err != nil || doc.Issuer == "" {
		return "", nil, fmt.Errorf("service account issuer discovery: no issuer")
	}
	jwks, err := rest.Get().AbsPath(oidcJWKSPath).DoRaw(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("service account jwks: %w", err)
	}
	if !json.Valid(jwks) {
		return "", nil, fmt.Errorf("service account jwks: invalid JSON")
	}
	return doc.Issuer, jwks, nil
}

// serviceAccountKeysLoop : publie au démarrage, puis à chaque changement
// (rotation des clés de l'apiserver) et au moins toutes les heures.
func (m *Manager) serviceAccountKeysLoop(ctx context.Context) {
	var lastHash [32]byte
	var lastSent time.Time
	publish := func() {
		issuer, jwks, err := m.serviceAccountKeys(ctx)
		if err != nil {
			m.log.Warnw("cannot read service account keys (workload identity unavailable)", "err", err)
			return
		}
		h := sha256.Sum256(append([]byte(issuer+"\n"), jwks...))
		if h == lastHash && time.Since(lastSent) < keysRepublishAll {
			return
		}
		if err := m.restateClient.ReportServiceAccountKeys(ctx, issuer, jwks); err != nil {
			m.log.Warnw("failed to publish service account keys", "err", err)
			return
		}
		lastHash, lastSent = h, time.Now()
		m.log.Infow("service account keys published", "issuer", issuer)
	}
	publish()
	ticker := time.NewTicker(keysCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}
