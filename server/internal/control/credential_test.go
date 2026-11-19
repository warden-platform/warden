package control

import (
	"bytes"
	"testing"
)

func TestCredentialCipherRoundTrip(t *testing.T) {
	cipher, err := newCredentialCipher([]byte("test-master-key-with-at-least-32-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("connector-specific-task-key-32bytes")
	encrypted, err := cipher.Encrypt(original)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, original) {
		t.Fatal("ciphertext contains plaintext credential")
	}
	decrypted, err := cipher.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, original) {
		t.Fatalf("decrypted credential = %q", decrypted)
	}
}

func TestPairingCodeFormatAndHash(t *testing.T) {
	code, err := randomPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 9 || code[4] != '-' {
		t.Fatalf("pairing code %q has unexpected format", code)
	}
	if hashPairingCode(code) != hashPairingCode("  "+code+"  ") {
		t.Fatal("pairing code hashing should ignore surrounding whitespace")
	}
	if hashBootstrapToken("AbC") == hashBootstrapToken("aBc") {
		t.Fatal("bootstrap token hashing must remain case-sensitive")
	}
	if hashLegacyBootstrapToken("AbC") != hashLegacyBootstrapToken("aBc") {
		t.Fatal("legacy bootstrap token hashing must support migration from case-insensitive hashes")
	}
	if hashBootstrapToken("AbC") == hashLegacyBootstrapToken("AbC") {
		t.Fatal("mixed-case bootstrap tokens must migrate to a distinct case-sensitive hash")
	}
}
