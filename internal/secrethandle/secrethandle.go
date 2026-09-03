// Package secrethandle implements the local-only plugin secret reference boundary.
// Server configuration carries only secret://<name> handles; plaintext is resolved
// on the target Edge for one tenant/instance and is never logged or persisted by
// this package.
package secrethandle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

const (
	Scheme          = "secret://"
	DefaultMaxBytes = 64 << 10
	maxNameBytes    = 64
)

var (
	ErrInvalidHandle      = errors.New("secrethandle: invalid handle")
	ErrUndeclaredSecret   = errors.New("secrethandle: secret is not declared by plugin")
	ErrUnsafeSecretPath   = errors.New("secrethandle: unsafe secret path")
	ErrInsecurePermission = errors.New("secrethandle: insecure secret file permission")
	ErrSecretTooLarge     = errors.New("secrethandle: secret exceeds size limit")
)

// Handle is a validated secret://<name> reference. It never contains a value or path.
type Handle struct{ name string }

func (h Handle) Name() string   { return h.name }
func (h Handle) String() string { return Scheme + h.name }

// Parse validates a stable secret name. Names are deliberately stricter than
// filesystem names so a handle can never encode a path or platform-specific trick.
func Parse(raw string) (Handle, error) {
	if !strings.HasPrefix(raw, Scheme) {
		return Handle{}, fmt.Errorf("%w: expected %s<name>", ErrInvalidHandle, Scheme)
	}
	name := strings.TrimPrefix(raw, Scheme)
	if !validSegment(name) {
		return Handle{}, fmt.Errorf("%w: invalid secret name", ErrInvalidHandle)
	}
	return Handle{name: name}, nil
}

// ValidateDeclared requires a matching manifest permissions.secrets entry.
func ValidateDeclared(h Handle, declared []string) error {
	for _, name := range declared {
		if name == h.name && validSegment(name) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrUndeclaredSecret, h.name)
}

// FileProvider resolves secrets from Root/<tenant>/<instance>/<name>. It does
// not cache plaintext. Callers should zero/discard the returned bytes promptly.
type FileProvider struct {
	Root     string
	MaxBytes int64
}

func (p FileProvider) Resolve(tenant, instance, rawHandle string, declared []string) ([]byte, error) {
	h, err := Parse(rawHandle)
	if err != nil {
		return nil, err
	}
	if err := ValidateDeclared(h, declared); err != nil {
		return nil, err
	}
	if !validSegment(tenant) || !validSegment(instance) {
		return nil, fmt.Errorf("%w: invalid tenant or instance", ErrUnsafeSecretPath)
	}
	root, err := filepath.Abs(strings.TrimSpace(p.Root))
	if err != nil || strings.TrimSpace(p.Root) == "" {
		return nil, fmt.Errorf("%w: invalid root", ErrUnsafeSecretPath)
	}
	path := filepath.Join(root, tenant, instance, h.name)
	if !within(root, path) {
		return nil, ErrUnsafeSecretPath
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve root: %v", ErrUnsafeSecretPath, err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve secret %s: %w", h.name, err)
	}
	if !within(resolvedRoot, resolvedPath) {
		return nil, ErrUnsafeSecretPath
	}

	info, err := os.Lstat(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("stat secret %s: %w", h.name, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnsafeSecretPath
	}
	// POSIX group/other permissions are fail-closed. Windows ACL validation is
	// platform-specific and belongs to a future provider; current-user files are
	// still isolated by the configured private root.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, ErrInsecurePermission
	}
	limit := p.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	if info.Size() > limit {
		return nil, ErrSecretTooLarge
	}
	f, err := os.Open(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("open secret %s: %w", h.name, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read secret %s: %w", h.name, err)
	}
	if int64(len(data)) > limit {
		return nil, ErrSecretTooLarge
	}
	return data, nil
}

func validSegment(s string) bool {
	if len(s) == 0 || len(s) > maxNameBytes || s == "." || s == ".." {
		return false
	}
	for i, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			if i == 0 && (r == '.' || r == '-') {
				return false
			}
			continue
		}
		return false
	}
	return !strings.ContainsAny(s, `/\:`)
}

func within(root, path string) bool {
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(r, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
