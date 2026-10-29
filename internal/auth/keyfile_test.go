package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeKeyFile(t *testing.T, tokenURI, cluster string) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	f := KeyFile{Type: keyFileType, ClusterID: cluster, Principal: "serviceAccount:1111", KeyID: "abc123",
		Algorithm: "ES256", PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), TokenURI: tokenURI}
	raw, _ := json.Marshal(f)
	path := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, key
}

// Faux STS : vérifie l'assertion (signature ES256 R||S, claims) comme betorun.
func fakeSTS(t *testing.T, pub *ecdsa.PublicKey, calls *atomic.Int32, uri *string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != grantJWTBearer {
			http.Error(w, `{"error":"unsupported_grant_type"}`, 400)
			return
		}
		parts := strings.Split(r.PostForm.Get("assertion"), ".")
		if len(parts) != 3 {
			t.Fatalf("assertion malformée")
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if len(sig) != 64 || !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			t.Fatalf("signature invalide")
		}
		var hdr, claims map[string]any
		h, _ := base64.RawURLEncoding.DecodeString(parts[0])
		c, _ := base64.RawURLEncoding.DecodeString(parts[1])
		_ = json.Unmarshal(h, &hdr)
		_ = json.Unmarshal(c, &claims)
		if hdr["kid"] != "abc123" || hdr["alg"] != "ES256" || claims["iss"] != "serviceAccount:1111" ||
			claims["sub"] != claims["iss"] || claims["aud"] != *uri || claims["jti"] == "" {
			t.Fatalf("assertion inattendue : %v %v", hdr, claims)
		}
		if claims["exp"].(float64)-claims["iat"].(float64) > 3600 {
			t.Fatalf("assertion trop longue")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-" + string(rune('0'+calls.Load())), "token_type": "Bearer", "expires_in": 3600})
	}))
}

func TestTokenSource(t *testing.T) {
	var calls atomic.Int32
	var uri string
	path, key := writeKeyFile(t, "", "k3s-dakar")
	srv := fakeSTS(t, &key.PublicKey, &calls, &uri)
	defer srv.Close()
	uri = srv.URL + "/token"
	// Réécrit le fichier avec la bonne URI (le serveur n'existait pas encore).
	raw, _ := os.ReadFile(path)
	var f KeyFile
	_ = json.Unmarshal(raw, &f)
	f.TokenURI = uri
	raw, _ = json.Marshal(f)
	_ = os.WriteFile(path, raw, 0o600)

	if _, err := LoadKeyFile(path, "autre-cluster", nil); err == nil {
		t.Fatal("un fichier d'un autre cluster doit être refusé")
	}
	ts, err := LoadKeyFile(path, "k3s-dakar", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tok1, err := ts.Token(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tok2, _ := ts.Token(ctx)
	if tok1 != tok2 || calls.Load() != 1 {
		t.Fatalf("jeton non mis en cache (%d appels)", calls.Load())
	}
	// Proche de l'expiration : renouvelé.
	ts.now = func() time.Time { return time.Now().Add(56 * time.Minute) }
	if _, err := ts.Token(ctx); err != nil || calls.Load() != 2 {
		t.Fatalf("jeton non renouvelé avant expiration (%d appels, %v)", calls.Load(), err)
	}
	ts.now = time.Now
	// 401 côté API : invalidation, nouveau jeton.
	ts.Invalidate()
	req, _ := http.NewRequest("GET", "http://x", nil)
	if err := ts.Apply(ctx, req); err != nil || calls.Load() != 3 {
		t.Fatalf("pas de nouveau jeton après Invalidate (%d appels, %v)", calls.Load(), err)
	}
	if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer tok-") {
		t.Fatalf("en-tête : %q", req.Header.Get("Authorization"))
	}
}

func TestTokenSource_STSError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"the key is expired"}`))
	}))
	defer srv.Close()
	path, _ := writeKeyFile(t, srv.URL+"/token", "")
	ts, err := LoadKeyFile(path, "any", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ts.Token(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the key is expired") {
		t.Fatalf("erreur du STS non remontée : %v", err)
	}
}
