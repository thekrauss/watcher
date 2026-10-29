package restate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

type countingAuth struct{ applied, invalidated atomic.Int32 }

func (a *countingAuth) Apply(_ context.Context, r *http.Request) error {
	a.applied.Add(1)
	r.Header.Set("Authorization", "Bearer t")
	return nil
}
func (a *countingAuth) Invalidate() { a.invalidated.Add(1) }

// Un 401 (jeton expiré, clé tournée) renouvelle l'authentification et
// réessaie : l'événement n'est pas perdu.
func TestNotifyPodEvent_RetriesAfter401(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	a := &countingAuth{}
	c := NewRestateClient(srv.URL, "k3s", a, 2*time.Second, zap.NewNop().Sugar())
	if err := c.NotifyPodEvent(context.Background(), PodEvent{EventID: "e1"}); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || a.invalidated.Load() != 1 || a.applied.Load() != 2 {
		t.Fatalf("hits=%d invalidated=%d applied=%d", hits.Load(), a.invalidated.Load(), a.applied.Load())
	}
}
