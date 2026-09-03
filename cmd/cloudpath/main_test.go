package main

import (
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

func TestErrorCodeStable(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{registry.ErrInvalidManifest, "ERR_INVALID_MANIFEST"},
		{registry.ErrDigestMismatch, "ERR_DIGEST_MISMATCH"},
		{registry.ErrDigestUnavailable, "ERR_DIGEST_UNAVAILABLE"},
		{registry.ErrCoreIncompatible, "ERR_CORE_INCOMPATIBLE"},
		{registry.ErrProtocolIncompatible, "ERR_PROTOCOL_INCOMPATIBLE"},
		{registry.ErrHostRuntimeUnavailable, "ERR_HOST_RUNTIME_UNAVAILABLE"},
		{registry.ErrPermissionConfirmationRequired, "ERR_PERMISSION_CONFIRMATION_REQUIRED"},
		{registry.ErrRateLimited, "ERR_RATE_LIMITED"},
		{registry.ErrUnsafeArtifact, "ERR_UNSAFE_ARTIFACT"},
		{registry.ErrUnsupportedSource, "ERR_NOT_FOUND"},
		{registry.ErrNotFound, "ERR_NOT_FOUND"},
		{plugincontrol.ErrNotFound, "ERR_NOT_FOUND"},
		{plugincontrol.ErrInvalidState, "ERR_INVALID_STATE"},
		{plugincontrol.ErrPermissionConfirmationRequired, "ERR_PERMISSION_CONFIRMATION_REQUIRED"},
	}
	for _, tc := range cases {
		if got := errorCode(tc.err); got != tc.code {
			t.Fatalf("errorCode(%v) = %q, want %q", tc.err, got, tc.code)
		}
	}
}

func TestInstallErrorCodeStable(t *testing.T) {
	for _, err := range []error{
		registry.ErrInvalidManifest,
		registry.ErrCoreIncompatible,
		registry.ErrProtocolIncompatible,
		registry.ErrDigestMismatch,
		registry.ErrDigestUnavailable,
		registry.ErrPermissionConfirmationRequired,
	} {
		if got := installErrorCode(err); got != 3 {
			t.Fatalf("installErrorCode(%v) = %d, want 3", err, got)
		}
	}
	if got := installErrorCode(registry.ErrUnsafeArtifact); got != 1 {
		t.Fatalf("installErrorCode(%v) = %d, want 1", registry.ErrUnsafeArtifact, got)
	}
	if got := installErrorCode(registry.ErrRateLimited); got != 1 {
		t.Fatalf("installErrorCode(%v) = %d, want 1", registry.ErrRateLimited, got)
	}
}

func TestRedactSecrets(t *testing.T) {
	secret := "ghp_super_secret_token_value_123456"
	t.Setenv("GITHUB_TOKEN", secret)
	out := redactSecrets("failed https://github.com/owner/repo using " + secret + " and Authorization: Bearer " + secret)
	if strings.Contains(out, secret) {
		t.Fatalf("redactSecrets leaked the token: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("redactSecrets should mark secrets as redacted: %q", out)
	}
	// GH_TOKEN is also considered.
	t.Setenv("GITHUB_TOKEN", "")
	ghSecret := "gho_another_secret"
	t.Setenv("GH_TOKEN", ghSecret)
	out = redactSecrets("Authorization: Bearer " + ghSecret)
	if strings.Contains(out, ghSecret) {
		t.Fatalf("redactSecrets leaked GH_TOKEN: %q", out)
	}
}

func TestRunUnknownCommandIsNonZero(t *testing.T) {
	if code := run([]string{"bogus"}); code == 0 {
		t.Fatal("unknown command should return a non-zero code")
	}
}
