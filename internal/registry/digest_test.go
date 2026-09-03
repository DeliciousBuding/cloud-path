package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDigestVerify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.bin")
	data := []byte("cloudpath plugin artifact")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigest(path, digest); err != nil {
		t.Fatalf("bare digest: %v", err)
	}
	if err := VerifyDigest(path, "sha256:"+digest); err != nil {
		t.Fatalf("prefixed digest: %v", err)
	}
	if err := VerifyDigest(path, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong digest should fail")
	}
	if SHA256Bytes(data) != digest {
		t.Fatal("SHA256Bytes should match SHA256File")
	}
}
