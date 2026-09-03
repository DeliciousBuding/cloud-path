package application

import (
	"fmt"
	"strconv"
	"strings"
)

// capabilityRef is a parsed capability reference
// "<publisher>/capability/<name>@<version>". A version of 0 means "any
// version" (used when the requirement declares a bare base, which the manifest
// normally forbids).
type capabilityRef struct {
	base    string
	version int
}

// parseCapabilityRef splits a capability reference into base and version.
// It accepts "<base>@<version>" and bare "<base>".
func parseCapabilityRef(id string) (capabilityRef, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return capabilityRef{}, fmt.Errorf("capability reference must not be empty")
	}
	at := strings.LastIndex(id, "@")
	if at < 0 {
		return capabilityRef{base: id}, nil
	}
	verStr := id[at+1:]
	ver, err := strconv.Atoi(verStr)
	if err != nil {
		return capabilityRef{}, fmt.Errorf("capability %q: invalid version segment %q", id, verStr)
	}
	base := id[:at]
	if base == "" {
		return capabilityRef{}, fmt.Errorf("capability %q: missing base before @", id)
	}
	return capabilityRef{base: base, version: ver}, nil
}

// capabilityCompatible reports whether a candidate capability satisfies a
// required capability. Compatibility is purely on capability base and version:
// the base must match exactly, and the candidate version must be >= the
// required version (a newer version is backward compatible; an older one is
// not). It never inspects driver, device, port or hardware.
//
// The returned code is CodeCapabilityMismatch when the base differs and
// CodeVersionIncompatible when the base matches but the version is too old.
func capabilityCompatible(required, candidate string) (bool, string) {
	req, err := parseCapabilityRef(required)
	if err != nil {
		return false, CodeInvalidCapability
	}
	cand, err := parseCapabilityRef(candidate)
	if err != nil {
		return false, CodeInvalidCapability
	}
	if req.base != cand.base {
		return false, CodeCapabilityMismatch
	}
	if req.version != 0 && cand.version < req.version {
		return false, CodeVersionIncompatible
	}
	return true, ""
}

// candidateProvidesCapability checks whether a candidate exposes a capability
// compatible with the requirement. It returns CodeVersionIncompatible when no
// provided capability is compatible but at least one shares the base yet is too
// old; otherwise it returns CodeCapabilityMismatch. A compatible capability
// anywhere on the entity wins.
func candidateProvidesCapability(c Candidate, required string) (bool, string, string) {
	versionConflict := false
	for _, cap := range c.Capabilities {
		ok, code := capabilityCompatible(required, cap)
		if ok {
			return true, "", ""
		}
		if code == CodeVersionIncompatible {
			versionConflict = true
		}
	}
	if versionConflict {
		return false, CodeVersionIncompatible, fmt.Sprintf(
			"entity %q provides an incompatible version of required %q", c.EntityID, required)
	}
	return false, CodeCapabilityMismatch, fmt.Sprintf(
		"entity %q does not provide a capability matching %q", c.EntityID, required)
}
