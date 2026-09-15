package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestRunGeneratesCompatibleKeys(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	randomness := bytes.NewReader(make([]byte, 64))
	if err := run([]string{"-format", "json"}, &output, randomness); err != nil {
		t.Fatal(err)
	}
	var keys keySet
	if err := json.Unmarshal(output.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	signing, err := base64.RawStdEncoding.DecodeString(keys.SigningKey)
	if err != nil || len(signing) != 64 {
		t.Fatalf("signing key length = %d, error = %v", len(signing), err)
	}
	verifying, err := base64.RawStdEncoding.DecodeString(keys.VerifyingKey)
	if err != nil || len(verifying) != 32 {
		t.Fatalf("verifying key length = %d, error = %v", len(verifying), err)
	}
	audit, err := base64.RawStdEncoding.DecodeString(keys.AuditFingerprintKey)
	if err != nil || len(audit) != 32 {
		t.Fatalf("audit key length = %d, error = %v", len(audit), err)
	}
}
