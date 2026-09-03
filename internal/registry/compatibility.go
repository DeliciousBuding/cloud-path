package registry

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var versionPattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+].*)?$`)

type semver struct {
	Major int
	Minor int
	Patch int
}

// CheckCoreCompatibility verifies compatibility.core against current version.
// It supports operators >=, <=, >, <, =, ^ and ~, joined by whitespace or commas.
func CheckCoreCompatibility(manifest *Manifest, current string) error {
	if manifest == nil {
		return fmt.Errorf("%w: manifest is nil", ErrCoreIncompatible)
	}
	rangeStr := strings.TrimSpace(manifest.Compatibility.Core)
	if rangeStr == "" {
		return fmt.Errorf("%w: compatibility.core is required", ErrCoreIncompatible)
	}
	cur, err := parseSemver(current)
	if err != nil {
		return fmt.Errorf("parse current core version %q: %w", current, err)
	}
	for _, constraint := range strings.FieldsFunc(rangeStr, func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r)
	}) {
		ok, err := constraintMatches(constraint, cur)
		if err != nil {
			return fmt.Errorf("invalid compatibility.core %q: %w", rangeStr, err)
		}
		if !ok {
			return fmt.Errorf("%w: compatibility.core %q does not include %s", ErrCoreIncompatible, rangeStr, current)
		}
	}
	return nil
}

func constraintMatches(constraint string, current semver) (bool, error) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return false, fmt.Errorf("empty constraint")
	}
	if constraint == "*" || strings.EqualFold(constraint, "x") {
		return true, nil
	}
	op := ""
	rest := constraint
	for _, candidate := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(rest, candidate) {
			op = candidate
			rest = strings.TrimSpace(strings.TrimPrefix(rest, candidate))
			break
		}
	}
	target, err := parseSemver(rest)
	if err != nil {
		return false, fmt.Errorf("parse version %q: %w", rest, err)
	}
	cmp := compareSemver(current, target)
	switch op {
	case "":
		return cmp == 0, nil
	case "=":
		return cmp == 0, nil
	case ">=":
		return cmp >= 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	case "<":
		return cmp < 0, nil
	case "^":
		lower := compareSemver(current, target) >= 0
		upper := semver{}
		if target.Major == 0 {
			upper = semver{Major: 0, Minor: target.Minor + 1, Patch: 0}
		} else {
			upper = semver{Major: target.Major + 1}
		}
		return lower && compareSemver(current, upper) < 0, nil
	case "~":
		lower := compareSemver(current, target) >= 0
		upper := semver{Major: target.Major, Minor: target.Minor + 1}
		return lower && compareSemver(current, upper) < 0, nil
	default:
		return false, fmt.Errorf("unknown operator %q", op)
	}
}

func parseSemver(value string) (semver, error) {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return semver{}, fmt.Errorf("not a semantic version: %q", value)
	}
	parse := func(s string) int {
		n, _ := strconv.Atoi(s)
		return n
	}
	return semver{
		Major: parse(match[1]),
		Minor: parse(match[2]),
		Patch: parse(match[3]),
	}, nil
}

func compareSemver(a, b semver) int {
	if a.Major != b.Major {
		if a.Major < b.Major {
			return -1
		}
		return 1
	}
	if a.Minor != b.Minor {
		if a.Minor < b.Minor {
			return -1
		}
		return 1
	}
	if a.Patch != b.Patch {
		if a.Patch < b.Patch {
			return -1
		}
		return 1
	}
	return 0
}
