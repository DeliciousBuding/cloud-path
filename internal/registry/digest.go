package registry

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// SHA256Bytes returns the lowercase hex sha256 digest of data.
func SHA256Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SHA256File returns the lowercase hex sha256 digest of a file.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s for digest: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// NormalizeDigest accepts `sha256:<hex>`, `SHA256(...)=<hex>`, a bare hex
// digest, or a base64 SHA-256 digest from GitHub. It returns lowercase hex.
func NormalizeDigest(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", errors.New("digest is empty")
	}
	lower := strings.ToLower(v)
	switch {
	case strings.HasPrefix(lower, "sha256:"):
		v = strings.TrimSpace(v[len("sha256:"):])
	case strings.HasPrefix(lower, "sha256-"):
		v = strings.TrimSpace(v[len("sha256-"):])
		if decoded, err := base64.StdEncoding.DecodeString(v); err == nil && len(decoded) == sha256.Size {
			return hex.EncodeToString(decoded), nil
		}
	case strings.HasPrefix(lower, "sha256("):
		start := strings.IndexByte(v, '(')
		end := strings.IndexByte(v, ')')
		if start >= 0 && end > start {
			rest := strings.TrimSpace(v[end+1:])
			if strings.HasPrefix(rest, "=") {
				v = strings.TrimSpace(strings.TrimPrefix(rest, "="))
			}
		}
	case strings.HasPrefix(lower, "sha256sum:"):
		v = strings.TrimSpace(v[len("sha256sum:"):])
	}
	v = strings.ToLower(strings.TrimSpace(v))
	if len(v) != sha256.Size*2 {
		return "", fmt.Errorf("digest must be a %d-character sha256 hex string", sha256.Size*2)
	}
	if _, err := hex.DecodeString(v); err != nil {
		return "", fmt.Errorf("digest is not valid hex: %w", err)
	}
	return v, nil
}

// VerifyDigest compares a file's sha256 digest with expected, using the same
// accepted digest representations as NormalizeDigest.
func VerifyDigest(path, expected string) error {
	actual, err := SHA256File(path)
	if err != nil {
		return err
	}
	normalized, err := NormalizeDigest(expected)
	if err != nil {
		return err
	}
	if actual != normalized {
		return fmt.Errorf("digest mismatch: expected %s, got %s", normalized, actual)
	}
	return nil
}

// ParseChecksumLine parses common sha256sum / GitHub checksum formats.
func ParseChecksumLine(line string) (filename, digest string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", false
	}
	if len(fields[0]) == sha256.Size*2 && isHex(fields[0]) {
		return fields[len(fields)-1], fields[0], true
	}
	if strings.HasPrefix(fields[0], "sha256:") && len(fields[0]) == len("sha256:")+sha256.Size*2 {
		digest := fields[0][len("sha256:"):]
		return fields[len(fields)-1], digest, true
	}
	if strings.HasSuffix(fields[len(fields)-1], ":") {
		return "", "", false
	}
	if len(fields[0]) != sha256.Size*2 {
		// Try `filename: <hex>`.
		if strings.HasSuffix(fields[0], ":") && len(fields[1]) == sha256.Size*2 && isHex(fields[1]) {
			return strings.TrimSuffix(fields[0], ":"), fields[1], true
		}
	}
	return "", "", false
}

// ParseChecksumBlob searches a checksum file for the digest of filename.
func ParseChecksumBlob(data []byte, filename string) (string, bool) {
	want := strings.TrimSpace(filename)
	for _, line := range strings.Split(string(data), "\n") {
		gotName, digest, ok := ParseChecksumLine(line)
		if !ok {
			continue
		}
		if gotName == want || strings.HasSuffix(gotName, "/"+want) {
			return digest, true
		}
	}
	return "", false
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}
