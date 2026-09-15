package identity

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxProviderResponse = 1 << 20

// ProviderMetadata contains the OIDC fields needed for token verification.
type ProviderMetadata struct {
	Issuer  string `json:"issuer"`
	JWKSURL string `json:"jwks_uri"`
}

// DiscoverProvider loads and validates an OpenID Connect provider configuration.
func DiscoverProvider(ctx context.Context, client *http.Client, issuer string) (ProviderMetadata, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	issuerURL, err := url.Parse(issuer)
	if err != nil || issuerURL.Host == "" || (issuerURL.Scheme != "https" && !isLoopbackHTTP(issuerURL)) {
		return ProviderMetadata{}, errors.New("OIDC issuer must be an HTTPS URL")
	}
	metadataURL := issuer + "/.well-known/openid-configuration"
	var metadata ProviderMetadata
	if err := fetchJSON(ctx, client, metadataURL, &metadata); err != nil {
		return ProviderMetadata{}, fmt.Errorf("discover OIDC provider: %w", err)
	}
	if strings.TrimRight(metadata.Issuer, "/") != issuer || metadata.JWKSURL == "" {
		return ProviderMetadata{}, errors.New("OIDC provider metadata does not match issuer")
	}
	jwksURL, err := url.Parse(metadata.JWKSURL)
	if err != nil || jwksURL.Host == "" || (jwksURL.Scheme != "https" && !isLoopbackHTTP(jwksURL)) {
		return ProviderMetadata{}, errors.New("OIDC JWKS URI must be an HTTPS URL")
	}
	metadata.Issuer = issuer
	return metadata, nil
}

// RemoteKeySet caches public keys from an OIDC JWKS endpoint.
type RemoteKeySet struct {
	mu        sync.Mutex
	client    *http.Client
	url       string
	cacheTTL  time.Duration
	now       func() time.Time
	keys      map[string]crypto.PublicKey
	expiresAt time.Time
}

// NewRemoteKeySet creates a cached JWKS key source.
func NewRemoteKeySet(client *http.Client, jwksURL string, cacheTTL time.Duration) (*RemoteKeySet, error) {
	parsed, err := url.Parse(strings.TrimSpace(jwksURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !isLoopbackHTTP(parsed)) {
		return nil, errors.New("JWKS URI must be an HTTPS URL")
	}
	if cacheTTL <= 0 {
		return nil, errors.New("JWKS cache lifetime must be positive")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &RemoteKeySet{client: client, url: parsed.String(), cacheTTL: cacheTTL, now: time.Now}, nil
}

// PublicKey returns a cached key and refreshes the JWKS when needed.
func (s *RemoteKeySet) PublicKey(ctx context.Context, keyID string) (crypto.PublicKey, error) {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return nil, errors.New("key ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if now.Before(s.expiresAt) {
		if key, ok := s.keys[keyID]; ok {
			return key, nil
		}
	}
	var document jwksDocument
	if err := fetchJSON(ctx, s.client, s.url, &document); err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	keys := make(map[string]crypto.PublicKey, len(document.Keys))
	for _, raw := range document.Keys {
		key, err := raw.publicKey()
		if err != nil || raw.KeyID == "" {
			continue
		}
		keys[raw.KeyID] = key
	}
	if len(keys) == 0 {
		return nil, errors.New("JWKS contains no supported keys")
	}
	s.keys = keys
	s.expiresAt = now.Add(s.cacheTTL)
	key, ok := keys[keyID]
	if !ok {
		return nil, fmt.Errorf("verification key %q not found", keyID)
	}
	return key, nil
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	KeyID     string `json:"kid"`
	KeyType   string `json:"kty"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	Curve     string `json:"crv"`
	X         string `json:"x"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
}

func (j jwk) publicKey() (crypto.PublicKey, error) {
	if j.Use != "" && j.Use != "sig" {
		return nil, errors.New("key is not a signing key")
	}
	switch j.KeyType {
	case "OKP":
		if j.Curve != "Ed25519" || (j.Algorithm != "" && j.Algorithm != "EdDSA") {
			return nil, errors.New("unsupported OKP key")
		}
		key, err := base64.RawURLEncoding.DecodeString(j.X)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("invalid Ed25519 key")
		}
		return ed25519.PublicKey(key), nil
	case "RSA":
		if j.Algorithm != "" && j.Algorithm != "RS256" {
			return nil, errors.New("unsupported RSA key")
		}
		modulus, err := base64.RawURLEncoding.DecodeString(j.Modulus)
		if err != nil {
			return nil, errors.New("invalid RSA modulus")
		}
		exponentBytes, err := base64.RawURLEncoding.DecodeString(j.Exponent)
		if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
			return nil, errors.New("invalid RSA exponent")
		}
		exponent := 0
		for _, value := range exponentBytes {
			exponent = exponent<<8 | int(value)
		}
		if exponent < 3 || exponent%2 == 0 {
			return nil, errors.New("invalid RSA exponent")
		}
		publicKey := &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponent}
		if publicKey.N.BitLen() < 2048 {
			return nil, errors.New("RSA key is too small")
		}
		return publicKey, nil
	default:
		return nil, errors.New("unsupported key type")
	}
}

func fetchJSON(ctx context.Context, client *http.Client, endpoint string, target any) error {
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxProviderResponse))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func isLoopbackHTTP(parsed *url.URL) bool {
	host := parsed.Hostname()
	return parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
}
