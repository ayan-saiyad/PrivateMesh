// Package identity validates user tokens and signs short-lived service principals.
package identity

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxTokenSize = 16 << 10

var (
	// ErrInvalidToken indicates that a token is malformed or has invalid claims.
	ErrInvalidToken = errors.New("invalid identity token")
	// ErrTokenExpired indicates that a token is outside its accepted lifetime.
	ErrTokenExpired = errors.New("identity token expired")
)

// Principal is the authenticated identity propagated with a request.
type Principal struct {
	Subject string
	Groups  []string
	Scopes  []string
}

// PublicKeySource resolves token verification keys by key ID.
type PublicKeySource interface {
	PublicKey(ctx context.Context, keyID string) (crypto.PublicKey, error)
}

// StaticKeySet is an in-memory verification key source.
type StaticKeySet map[string]crypto.PublicKey

// PublicKey returns a configured key.
func (s StaticKeySet) PublicKey(_ context.Context, keyID string) (crypto.PublicKey, error) {
	key, ok := s[keyID]
	if !ok {
		return nil, fmt.Errorf("verification key %q not found", keyID)
	}
	return key, nil
}

// Verifier validates signed tokens for one issuer and audience.
type Verifier struct {
	issuer   string
	audience string
	keys     PublicKeySource
	skew     time.Duration
	now      func() time.Time
}

// NewVerifier creates a token verifier.
func NewVerifier(issuer, audience string, keys PublicKeySource, skew time.Duration) (*Verifier, error) {
	issuer = strings.TrimSpace(issuer)
	audience = strings.TrimSpace(audience)
	if issuer == "" || audience == "" {
		return nil, errors.New("token issuer and audience are required")
	}
	if keys == nil {
		return nil, errors.New("verification keys are required")
	}
	if skew < 0 {
		return nil, errors.New("clock skew cannot be negative")
	}
	return &Verifier{issuer: issuer, audience: audience, keys: keys, skew: skew, now: time.Now}, nil
}

// Verify authenticates a compact JWT and returns its principal.
func (v *Verifier) Verify(ctx context.Context, token string) (Principal, error) {
	if len(token) == 0 || len(token) > maxTokenSize || strings.TrimSpace(token) != token {
		return Principal{}, ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Principal{}, ErrInvalidToken
	}
	headerBytes, err := decodePart(parts[0])
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	claimsBytes, err := decodePart(parts[1])
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	signature, err := decodePart(parts[2])
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	var header tokenHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.KeyID == "" {
		return Principal{}, ErrInvalidToken
	}
	key, err := v.keys.PublicKey(ctx, header.KeyID)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: verification key: %w", ErrInvalidToken, err)
	}
	if err := verifySignature(header.Algorithm, key, []byte(parts[0]+"."+parts[1]), signature); err != nil {
		return Principal{}, fmt.Errorf("%w: signature", ErrInvalidToken)
	}

	var claims tokenClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return Principal{}, ErrInvalidToken
	}
	if err := v.validateClaims(claims); err != nil {
		return Principal{}, err
	}
	return Principal{
		Subject: strings.TrimSpace(claims.Subject),
		Groups:  normalizeValues(claims.Groups),
		Scopes:  normalizeValues(strings.Fields(claims.Scope)),
	}, nil
}

func (v *Verifier) validateClaims(claims tokenClaims) error {
	now := v.now()
	if claims.Issuer != v.issuer || !claims.Audience.Contains(v.audience) || strings.TrimSpace(claims.Subject) == "" {
		return ErrInvalidToken
	}
	if claims.ExpiresAt == 0 || !now.Before(time.Unix(claims.ExpiresAt, 0).Add(v.skew)) {
		return ErrTokenExpired
	}
	if claims.NotBefore != 0 && now.Add(v.skew).Before(time.Unix(claims.NotBefore, 0)) {
		return ErrInvalidToken
	}
	if claims.IssuedAt != 0 && now.Add(v.skew).Before(time.Unix(claims.IssuedAt, 0)) {
		return ErrInvalidToken
	}
	return nil
}

// Signer creates short-lived Ed25519 principal tokens for service-to-service propagation.
type Signer struct {
	issuer   string
	audience string
	keyID    string
	key      ed25519.PrivateKey
	lifetime time.Duration
	now      func() time.Time
}

// NewSigner creates a principal token signer.
func NewSigner(issuer, audience, keyID string, key ed25519.PrivateKey, lifetime time.Duration) (*Signer, error) {
	issuer = strings.TrimSpace(issuer)
	audience = strings.TrimSpace(audience)
	keyID = strings.TrimSpace(keyID)
	if issuer == "" || audience == "" || keyID == "" {
		return nil, errors.New("token issuer, audience, and key ID are required")
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Ed25519 private key")
	}
	if lifetime <= 0 {
		return nil, errors.New("token lifetime must be positive")
	}
	return &Signer{issuer: issuer, audience: audience, keyID: keyID, key: key, lifetime: lifetime, now: time.Now}, nil
}

// Sign creates a signed principal token.
func (s *Signer) Sign(principal Principal) (string, error) {
	principal.Subject = strings.TrimSpace(principal.Subject)
	if principal.Subject == "" {
		return "", errors.New("principal subject is required")
	}
	now := s.now().UTC()
	header, err := encodeJSON(tokenHeader{Algorithm: "EdDSA", KeyID: s.keyID, Type: "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := encodeJSON(tokenClaims{
		Issuer:    s.issuer,
		Audience:  audience{s.audience},
		Subject:   principal.Subject,
		Groups:    normalizeValues(principal.Groups),
		Scope:     strings.Join(normalizeValues(principal.Scopes), " "),
		IssuedAt:  now.Unix(),
		NotBefore: now.Unix(),
		ExpiresAt: now.Add(s.lifetime).Unix(),
	})
	if err != nil {
		return "", err
	}
	unsigned := header + "." + claims
	signature, err := s.key.Sign(rand.Reader, []byte(unsigned), crypto.Hash(0))
	if err != nil {
		return "", fmt.Errorf("sign principal: %w", err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

type tokenHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ,omitempty"`
}

type tokenClaims struct {
	Issuer    string   `json:"iss"`
	Audience  audience `json:"aud"`
	Subject   string   `json:"sub"`
	Groups    []string `json:"groups,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	IssuedAt  int64    `json:"iat"`
	NotBefore int64    `json:"nbf"`
	ExpiresAt int64    `json:"exp"`
}

type audience []string

// UnmarshalJSON accepts the string and array audience forms allowed by JWT.
func (a *audience) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = audience{single}
		return nil
	}
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return err
	}
	*a = multiple
	return nil
}

func (a audience) Contains(value string) bool {
	for _, candidate := range a {
		if candidate == value {
			return true
		}
	}
	return false
}

func verifySignature(algorithm string, key crypto.PublicKey, message, signature []byte) error {
	switch algorithm {
	case "EdDSA":
		publicKey, ok := key.(ed25519.PublicKey)
		if !ok || !ed25519.Verify(publicKey, message, signature) {
			return errors.New("invalid Ed25519 signature")
		}
		return nil
	case "RS256":
		publicKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return errors.New("invalid RSA key")
		}
		digest := sha256.Sum256(message)
		return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature)
	default:
		return errors.New("unsupported signing algorithm")
	}
}

func encodeJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodePart(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func normalizeValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}
