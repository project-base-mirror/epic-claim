package secret

import (
	"bytes"
	"testing"
)

func TestRoundTripAndTamper(t *testing.T) {
	k, err := ParseKey("MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := New(k)
	blob, err := b.Seal([]byte("hunter2"), []byte("account:1"))
	if err != nil || bytes.Contains(blob, []byte("hunter2")) {
		t.Fatal("plaintext leaked", err)
	}
	if p, err := b.Open(blob, []byte("account:1")); err != nil || string(p) != "hunter2" {
		t.Fatal(p, err)
	}
	if _, err := b.Open(blob, []byte("account:2")); err == nil {
		t.Fatal("aad not bound")
	}
	blob[len(blob)-1] ^= 1
	if _, err := b.Open(blob, []byte("account:1")); err == nil {
		t.Fatal("tamper accepted")
	}
	if _, err := ParseKey("short"); err == nil {
		t.Fatal("short key accepted")
	}
}
