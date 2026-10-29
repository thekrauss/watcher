// Package auth : authentification du watcher auprès de betorun.
//
// Deux modes :
//   - fichier de clé (recommandé) : la clé privée du compte de service du
//     cluster signe une assertion échangée contre un jeton d'1 h auprès du
//     STS (RFC 7523). Aucun secret ne transite : seul le jeton court circule.
//   - clé API (btr_live_… ou ancienne km_…), envoyée telle quelle.
package auth

import (
	"context"
	"net/http"
)

// Authenticator pose l'authentification sur une requête sortante.
type Authenticator interface {
	Apply(ctx context.Context, req *http.Request) error
	// Invalidate : la requête a été refusée (401) ; le prochain Apply
	// renouvelle l'authentification si c'est possible.
	Invalidate()
}

// None : pas d'authentification (dev local contre un backend sans auth).
type None struct{}

func (None) Apply(context.Context, *http.Request) error { return nil }
func (None) Invalidate()                                {}

// APIKey : en-tête X-API-Key.
type APIKey string

func (k APIKey) Apply(_ context.Context, req *http.Request) error {
	req.Header.Set("X-API-Key", string(k))
	return nil
}
func (APIKey) Invalidate() {}
