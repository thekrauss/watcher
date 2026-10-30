package auth

import "time"

// En-têtes HTTP posés sur les requêtes vers le backend.
const (
	headerAPIKey        = "X-API-Key"
	headerAuthorization = "Authorization"
	headerContentType   = "Content-Type"
	bearerPrefix        = "Bearer "
	mimeFormURLEncoded  = "application/x-www-form-urlencoded"
)

// Assertion JWT (RFC 7523) signée avec la clé du compte de service.
const (
	algES256 = "ES256"
	typJWT   = "JWT"
	// jtiBytes : octets aléatoires de l'identifiant unique de l'assertion.
	jtiBytes = 16
	// es256CoordLen : taille de R et de S dans une signature ES256.
	es256CoordLen = 32
	// stsHTTPTimeout : délai d'un échange auprès du STS.
	stsHTTPTimeout = 10 * time.Second
)
