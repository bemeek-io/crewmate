package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func newTestBox(t *testing.T) *Box {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	b, err := NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	b := newTestBox(t)
	ct, err := b.Encrypt([]byte("secret-token"), []byte("row-id"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := b.Decrypt(ct, []byte("row-id"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, []byte("secret-token")) {
		t.Fatalf("got %q", pt)
	}
}

func TestWrongAADFails(t *testing.T) {
	b := newTestBox(t)
	ct, _ := b.Encrypt([]byte("secret"), []byte("row-a"))
	if _, err := b.Decrypt(ct, []byte("row-b")); err == nil {
		t.Fatal("decrypt with wrong AAD should fail")
	}
}

func TestTamperFails(t *testing.T) {
	b := newTestBox(t)
	ct, _ := b.Encrypt([]byte("secret"), nil)
	ct[len(ct)-1] ^= 0xff
	if _, err := b.Decrypt(ct, nil); err == nil {
		t.Fatal("decrypt of tampered ciphertext should fail")
	}
}

func TestWrongKeyLength(t *testing.T) {
	if _, err := NewBox(make([]byte, 16)); err == nil {
		t.Fatal("16-byte key should be rejected")
	}
}
