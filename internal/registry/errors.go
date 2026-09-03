package registry

import "errors"

// Sentinels used by the CLI to translate failures into stable error codes.
var (
	// ErrInvalidManifest means the root plugin.yaml failed schema validation.
	ErrInvalidManifest = errors.New("invalid plugin manifest")
	// ErrCoreIncompatible means compatibility.core does not cover current Core.
	ErrCoreIncompatible = errors.New("core version incompatible")
	// ErrProtocolIncompatible means the plugin declares an unsupported protocol.
	ErrProtocolIncompatible = errors.New("plugin protocol incompatible")
	// ErrDigestUnavailable means no expected digest was found on the release.
	ErrDigestUnavailable = errors.New("release asset digest unavailable")
	// ErrDigestMismatch means the downloaded asset digest does not match.
	ErrDigestMismatch = errors.New("release asset digest mismatch")
	// ErrInvalidDigest means --digest was not a valid sha256 digest.
	ErrInvalidDigest = errors.New("invalid digest")
	// ErrTrustConfirmationRequired means install has no independent digest,
	// verified Registry entry or attestation, and unreviewed TOFU was not
	// explicitly allowed.
	ErrTrustConfirmationRequired = errors.New("trust confirmation required")
	// ErrRegistryBindingMismatch means a verified Registry entry did not bind
	// plugin id/version/source/digest/publisher.
	ErrRegistryBindingMismatch = errors.New("registry binding mismatch")
	// ErrAttestationFailed means an attestation verifier did not authenticate
	// the artifact.
	ErrAttestationFailed = errors.New("attestation verification failed")
	// ErrTrustDowngrade means an update would replace a verified installation
	// with unreviewed TOFU or change source/publisher without confirmation.
	ErrTrustDowngrade = errors.New("trust downgrade rejected")
	// ErrUnsupportedSource means the source could not be resolved.
	ErrUnsupportedSource = errors.New("unsupported plugin source")
	// ErrNotFound means a repository, manifest or installed plugin was not found.
	ErrNotFound = errors.New("plugin not found")
	// ErrHostRuntimeUnavailable means instance operations require A4 Plugin Host.
	ErrHostRuntimeUnavailable = errors.New("plugin host runtime unavailable")
	// ErrPermissionConfirmationRequired means the user did not confirm permissions.
	ErrPermissionConfirmationRequired = errors.New("permission confirmation required")
	// ErrRateLimited means the GitHub API rate limit was exceeded.
	ErrRateLimited = errors.New("github api rate limit exceeded")
	// ErrUnsafeArtifact means a plugin id, asset name or download URL would escape
	// the plugin data root or use an unsupported scheme.
	ErrUnsafeArtifact = errors.New("unsafe plugin artifact")
)
