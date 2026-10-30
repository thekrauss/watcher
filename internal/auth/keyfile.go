package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	keyFileType    = "betorun_service_account_key"
	grantJWTBearer = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	// assertionTTL : durée de vie de l'assertion envoyée au STS.
	assertionTTL = 5 * time.Minute
	// refreshBefore : le jeton est renouvelé avant son expiration.
	refreshBefore = 5 * time.Minute
)

// KeyFile : fichier remis par betorun à la création de la clé.
type KeyFile struct {
	Type       string `json:"type"`
	ClusterID  string `json:"cluster_id"`
	Principal  string `json:"principal"`
	KeyID      string `json:"key_id"`
	Algorithm  string `json:"algorithm"`
	PrivateKey string `json:"private_key"`
	TokenURI   string `json:"token_uri"`
}

// TokenSource : jetons courts obtenus avec le fichier de clé, mis en cache.
type TokenSource struct {
	file KeyFile
	key  *ecdsa.PrivateKey
	http *http.Client
	now  func() time.Time

	mu    sync.Mutex
	token string
	exp   time.Time
}

// LoadKeyFile lit et valide le fichier ; clusterID doit être celui du fichier.
func LoadKeyFile(path, clusterID string, client *http.Client) (*TokenSource, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	var f KeyFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse key file: %w", err)
	}
	switch {
	case f.Type != keyFileType:
		return nil, fmt.Errorf("key file: type must be %q", keyFileType)
	case f.Algorithm != algES256:
		return nil, fmt.Errorf("key file: unsupported algorithm %q", f.Algorithm)
	case f.KeyID == "" || f.Principal == "" || f.TokenURI == "":
		return nil, errors.New("key file: key_id, principal and token_uri are required")
	case f.ClusterID != "" && f.ClusterID != clusterID:
		return nil, fmt.Errorf("key file belongs to cluster %q, not %q", f.ClusterID, clusterID)
	}
	block, _ := pem.Decode([]byte(f.PrivateKey))
	if block == nil {
		return nil, errors.New("key file: private_key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("key file: private key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("key file: not an ECDSA key")
	}
	if client == nil {
		client = &http.Client{Timeout: stsHTTPTimeout}
	}
	return &TokenSource{file: f, key: key, http: client, now: time.Now}, nil
}

// Apply : Authorization: Bearer <jeton court>.
func (s *TokenSource) Apply(ctx context.Context, req *http.Request) error {
	tok, err := s.Token(ctx)
	if err != nil {
		return err
	}
	req.Header.Set(headerAuthorization, bearerPrefix+tok)
	return nil
}

// Invalidate : jeton refusé (clé désactivée, rotation…) : on en redemande un.
func (s *TokenSource) Invalidate() {
	s.mu.Lock()
	s.token = ""
	s.mu.Unlock()
}

// Token : jeton en cache, ou nouveau jeton obtenu auprès du STS.
func (s *TokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && s.now().Add(refreshBefore).Before(s.exp) {
		return s.token, nil
	}
	assertion, err := s.assertion()
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {grantJWTBearer}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.file.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set(headerContentType, mimeFormURLEncoded)
	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("sts: %w", err)
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int    `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("sts: status %d: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		return "", fmt.Errorf("sts: %s: %s (status %d)", body.Error, body.ErrorDescription, resp.StatusCode)
	}
	s.token = body.AccessToken
	s.exp = s.now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return s.token, nil
}

// assertion : JWT ES256 signé avec la clé privée (RFC 7523).
func (s *TokenSource) assertion() (string, error) {
	now := s.now()
	jti := make([]byte, jtiBytes)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	header, _ := json.Marshal(map[string]string{"alg": algES256, "typ": typJWT, "kid": s.file.KeyID})
	claims, _ := json.Marshal(map[string]any{
		"iss": s.file.Principal, "sub": s.file.Principal, "aud": s.file.TokenURI,
		"iat": now.Unix(), "exp": now.Add(assertionTTL).Unix(), "jti": hex.EncodeToString(jti),
	})
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	r, sig, err := ecdsa.Sign(rand.Reader, s.key, digest[:])
	if err != nil {
		return "", err
	}
	// Signature JWS ES256 : R || S, 32 octets chacun.
	out := make([]byte, 2*es256CoordLen)
	r.FillBytes(out[:es256CoordLen])
	sig.FillBytes(out[es256CoordLen:])
	return signingInput + "." + enc.EncodeToString(out), nil
}
