// Package appversion compares Sub2API release versions. A pN suffix is a
// post-release patch, not a SemVer prerelease or build metadata suffix.
// Client identities and plugin package versions retain their own SemVer rules.
package appversion

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

var postReleasePattern = regexp.MustCompile(`^(v[0-9]+\.[0-9]+\.[0-9]+)p([1-9][0-9]*)([-+].*)?$`)

type version struct {
	core, post, prerelease, build string
}

func parse(raw string) (version, error) {
	v := strings.TrimSpace(raw)
	if v == "" || len(v) > 256 {
		return version{}, fmt.Errorf("invalid Sub2API version %q", raw)
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	post := ""
	if match := postReleasePattern.FindStringSubmatch(v); match != nil {
		post, v = match[2], match[1]+match[3]
	}
	if !semver.IsValid(v) {
		return version{}, fmt.Errorf("invalid Sub2API version %q", raw)
	}
	pre, build := semver.Prerelease(v), semver.Build(v)
	core := strings.TrimSuffix(semver.Canonical(v), pre)
	return version{core: core, post: post, prerelease: pre, build: build}, nil
}

// Normalize preserves the exact patch/prerelease/build identity, while making
// leading v, whitespace and SemVer short forms consistent.
func Normalize(raw string) (string, error) {
	v, err := parse(raw)
	if err != nil {
		return "", err
	}
	post := ""
	if v.post != "" {
		post = "p" + v.post
	}
	return v.core + post + v.prerelease + v.build, nil
}

// Compare orders the core version, numeric post-release patch, then prerelease.
// Build metadata does not affect precedence. Invalid inputs return an error,
// never an invented zero version. Decimal strings avoid integer overflow.
func Compare(a, b string) (int, error) {
	x, err := parse(a)
	if err != nil {
		return 0, err
	}
	y, err := parse(b)
	if err != nil {
		return 0, err
	}
	if c := semver.Compare(x.core, y.core); c != 0 {
		return c, nil
	}
	if len(x.post) < len(y.post) {
		return -1, nil
	}
	if len(x.post) > len(y.post) {
		return 1, nil
	}
	if c := strings.Compare(x.post, y.post); c != 0 {
		return c, nil
	}
	return semver.Compare(x.core+x.prerelease, y.core+y.prerelease), nil
}
