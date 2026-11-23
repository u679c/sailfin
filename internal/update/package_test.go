package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestVerifySignedPackage(t *testing.T) {
	key, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	placeholder := strings.Repeat("0", 128)
	raw := []byte("#!/bin/bash\nplatform='linux/amd64'\npackage_kind='update'\n# sailfin-signature: " +
		placeholder + "\n" + strings.Repeat("x", 512))
	digest := sha256.Sum256(raw)
	signed := bytes.Replace(raw, []byte(placeholder), []byte(hex.EncodeToString(ed25519.Sign(private, digest[:]))), 1)
	if err := Verify(bytes.NewReader(signed), key, "linux/amd64"); err != nil {
		t.Fatal(err)
	}
	if err := Verify(bytes.NewReader(signed), key, "linux/arm64"); err == nil {
		t.Fatal("signature must not authorize an update for another platform")
	}
	signed[len(signed)-1] ^= 1
	if err := Verify(bytes.NewReader(signed), key, "linux/amd64"); err == nil {
		t.Fatal("signature must cover the payload")
	}
}
