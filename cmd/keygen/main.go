// Command keygen creates coordinator signing and audit keys for a PrivateMesh deployment.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
)

type keySet struct {
	SigningKey          string `json:"principal_signing_key"`
	VerifyingKey        string `json:"principal_verifying_key"`
	AuditFingerprintKey string `json:"audit_fingerprint_key"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, rand.Reader); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string, output io.Writer, randomness io.Reader) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("format", "kubernetes", "output format: kubernetes or json")
	namespace := flags.String("namespace", "privatemesh", "Kubernetes namespace")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *format != "kubernetes" && *format != "json" {
		return errors.New("format must be kubernetes or json")
	}
	if *format == "kubernetes" && *namespace == "" {
		return errors.New("namespace is required for Kubernetes output")
	}
	publicKey, privateKey, err := ed25519.GenerateKey(randomness)
	if err != nil {
		return fmt.Errorf("generate Ed25519 key: %w", err)
	}
	auditKey := make([]byte, 32)
	if _, err := io.ReadFull(randomness, auditKey); err != nil {
		return fmt.Errorf("generate audit key: %w", err)
	}
	keys := keySet{
		SigningKey:          base64.RawStdEncoding.EncodeToString(privateKey),
		VerifyingKey:        base64.RawStdEncoding.EncodeToString(publicKey),
		AuditFingerprintKey: base64.RawStdEncoding.EncodeToString(auditKey),
	}
	if *format == "json" {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(keys)
	}
	_, err = fmt.Fprintf(output, `apiVersion: v1
kind: Secret
metadata:
  name: privatemesh-security
  namespace: %s
type: Opaque
stringData:
  principal-signing-key: %s
  principal-verifying-key: %s
  audit-fingerprint-key: %s
`, *namespace, strconv.Quote(keys.SigningKey), strconv.Quote(keys.VerifyingKey), strconv.Quote(keys.AuditFingerprintKey))
	return err
}
