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
	"reflect"
	"testing"
	"time"
)

func TestSignerAndVerifierPropagatePrincipal(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	signer, err := NewSigner("https://login.example.test", "search-nodes", "key-1", privateKey, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	signer.now = func() time.Time { return now }
	verifier, err := NewVerifier(
		"https://login.example.test",
		"search-nodes",
		StaticKeySet{"key-1": publicKey},
		30*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now.Add(time.Minute) }

	token, err := signer.Sign(Principal{
		Subject: " user-123 ",
		Groups:  []string{"engineering", "engineering", " "},
		Scopes:  []string{"documents.read", "search.execute"},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	want := Principal{
		Subject: "user-123",
		Groups:  []string{"engineering"},
		Scopes:  []string{"documents.read", "search.execute"},
	}
	if !reflect.DeepEqual(principal, want) {
		t.Fatalf("Verify() = %+v, want %+v", principal, want)
	}
}

func TestVerifierRejectsInvalidTokens(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	verifier, err := NewVerifier("issuer", "audience", StaticKeySet{"key-1": publicKey}, 0)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }

	tests := []struct {
		name   string
		claims tokenClaims
		key    ed25519.PrivateKey
		want   error
	}{
		{
			name: "expired",
			claims: tokenClaims{
				Issuer: "issuer", Audience: audience{"audience"}, Subject: "user",
				IssuedAt: now.Add(-2 * time.Minute).Unix(), ExpiresAt: now.Add(-time.Minute).Unix(),
			},
			key: privateKey, want: ErrTokenExpired,
		},
		{
			name: "wrong audience",
			claims: tokenClaims{
				Issuer: "issuer", Audience: audience{"other"}, Subject: "user",
				IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
			},
			key: privateKey, want: ErrInvalidToken,
		},
		{
			name: "future not before",
			claims: tokenClaims{
				Issuer: "issuer", Audience: audience{"audience"}, Subject: "user",
				IssuedAt: now.Unix(), NotBefore: now.Add(time.Minute).Unix(), ExpiresAt: now.Add(2 * time.Minute).Unix(),
			},
			key: privateKey, want: ErrInvalidToken,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			token := signedEdToken(t, "key-1", test.key, test.claims)
			if _, err := verifier.Verify(context.Background(), token); !errors.Is(err, test.want) {
				t.Fatalf("Verify() error = %v, want %v", err, test.want)
			}
		})
	}

	valid := signedEdToken(t, "key-1", privateKey, tokenClaims{
		Issuer: "issuer", Audience: audience{"audience"}, Subject: "user",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	})
	parts := splitToken(t, valid)
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"issuer","aud":"audience","sub":"attacker","exp":9999999999}`))
	tampered := parts[0] + "." + parts[1] + "." + parts[2]
	if _, err := verifier.Verify(context.Background(), tampered); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered Verify() error = %v, want %v", err, ErrInvalidToken)
	}
}

func TestVerifierAcceptsRS256OIDCToken(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	header, err := encodeJSON(tokenHeader{Algorithm: "RS256", KeyID: "rsa-1", Type: "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := encodeJSON(tokenClaims{
		Issuer: "https://accounts.example.test", Audience: audience{"other", "privatemesh"}, Subject: "user-1",
		Groups: []string{"research"}, Scope: "openid search.execute", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	unsigned := header + "." + claims
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	token := unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
	verifier, err := NewVerifier(
		"https://accounts.example.test",
		"privatemesh",
		StaticKeySet{"rsa-1": &privateKey.PublicKey},
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject != "user-1" || !reflect.DeepEqual(principal.Groups, []string{"research"}) {
		t.Fatalf("Verify() = %+v", principal)
	}
}

func signedEdToken(t *testing.T, keyID string, key ed25519.PrivateKey, claims tokenClaims) string {
	t.Helper()
	header, err := encodeJSON(tokenHeader{Algorithm: "EdDSA", KeyID: keyID, Type: "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(key, []byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func splitToken(t *testing.T, token string) []string {
	t.Helper()
	var parts []string
	start := 0
	for index, value := range token {
		if value == '.' {
			parts = append(parts, token[start:index])
			start = index + 1
		}
	}
	parts = append(parts, token[start:])
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	return parts
}
