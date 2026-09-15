package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOIDCDiscoveryAndRemoteKeyCache(t *testing.T) {
	t.Parallel()

	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = fmt.Fprintf(response, `{"issuer":%q,"jwks_uri":%q}`, server.URL, server.URL+"/keys")
		case "/keys":
			_, _ = fmt.Fprintf(response, `{"keys":[{"kid":"key-1","kty":"OKP","use":"sig","alg":"EdDSA","crv":"Ed25519","x":%q}]}`, base64.RawURLEncoding.EncodeToString(publicKey))
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	metadata, err := DiscoverProvider(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := NewRemoteKeySet(server.Client(), metadata.JWKSURL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first, err := keys.PublicKey(context.Background(), "key-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := keys.PublicKey(context.Background(), "key-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(first.(ed25519.PublicKey)) != string(publicKey) || string(second.(ed25519.PublicKey)) != string(publicKey) {
		t.Fatal("remote key does not match provider key")
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("HTTP requests = %d, want one discovery and one JWKS request", got)
	}
}

func TestOIDCDiscoveryRejectsMismatchedIssuer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"issuer":"https://other.example","jwks_uri":"https://other.example/keys"}`))
	}))
	t.Cleanup(server.Close)
	if _, err := DiscoverProvider(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("DiscoverProvider() error = nil, want issuer mismatch")
	}
}
