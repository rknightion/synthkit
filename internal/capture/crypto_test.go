// SPDX-License-Identifier: AGPL-3.0-only

package capture

import "testing"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	plain := []byte(`{"envelope":{"schema_version":1}}`)
	ct, err := Encrypt(plain, "hunter2")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if string(ct) == string(plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := Decrypt(ct, "hunter2")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, plain)
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	ct, _ := Encrypt([]byte("secret"), "right")
	if _, err := Decrypt(ct, "wrong"); err == nil {
		t.Fatal("expected error decrypting with wrong passphrase")
	}
}

func TestUnmarshalRejectsBadSchema(t *testing.T) {
	if _, err := Unmarshal([]byte(`{"envelope":{"schema_version":999}}`)); err == nil {
		t.Fatal("expected schema-version rejection")
	}
}
