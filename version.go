package main

import (
	"fmt"
	"strconv"
	"strings"
)

// These values are injected at build time via -ldflags "-X main.version=...".
// Local development builds keep the defaults below.
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

const appName = "nodeseek-rss-monitor"

// versionString returns the full version line used by --version and logs.
func versionString() string {
	return fmt.Sprintf("%s %s (commit %s, built %s)", appName, version, commit, buildTime)
}

// shortCommit returns a short commit hash for display.
func shortCommit() string {
	c := strings.TrimSpace(commit)
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

// semver is a minimal SemVer 2.0.0 representation. Build metadata is ignored
// for comparison, as required by the specification.
type semver struct {
	major int
	minor int
	patch int
	pre   []string
	raw   string
}

// parseSemver accepts "v1.2.3", "1.2.3", "1.2.3-rc.1" and "1.2.3+build".
func parseSemver(input string) (semver, error) {
	raw := strings.TrimSpace(input)
	s := strings.TrimPrefix(raw, "v")
	if s == "" {
		return semver{}, fmt.Errorf("版本号为空")
	}
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var pre []string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		if i == len(s)-1 {
			return semver{}, fmt.Errorf("版本号 %q 的预发布标识为空", raw)
		}
		pre = strings.Split(s[i+1:], ".")
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("版本号 %q 不是 major.minor.patch 格式", raw)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		if p == "" {
			return semver{}, fmt.Errorf("版本号 %q 包含空数字段", raw)
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, fmt.Errorf("版本号 %q 包含非法数字段 %q", raw, p)
		}
		nums[i] = n
	}
	return semver{major: nums[0], minor: nums[1], patch: nums[2], pre: pre, raw: raw}, nil
}

// compare returns -1, 0 or 1 following SemVer precedence rules.
func (v semver) compare(o semver) int {
	if c := compareInt(v.major, o.major); c != 0 {
		return c
	}
	if c := compareInt(v.minor, o.minor); c != 0 {
		return c
	}
	if c := compareInt(v.patch, o.patch); c != 0 {
		return c
	}
	// A version without prerelease has higher precedence.
	switch {
	case len(v.pre) == 0 && len(o.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(o.pre) == 0:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(o.pre); i++ {
		a, b := v.pre[i], o.pre[i]
		an, aerr := strconv.Atoi(a)
		bn, berr := strconv.Atoi(b)
		switch {
		case aerr == nil && berr == nil:
			if c := compareInt(an, bn); c != 0 {
				return c
			}
		case aerr == nil:
			return -1 // numeric identifiers sort before alphanumeric ones
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(a, b); c != 0 {
				return c
			}
		}
	}
	return compareInt(len(v.pre), len(o.pre))
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// compareVersions compares two version strings. It returns an error if either
// side is not a valid SemVer string.
func compareVersions(current, latest string) (int, error) {
	c, err := parseSemver(current)
	if err != nil {
		return 0, fmt.Errorf("当前版本无效: %w", err)
	}
	l, err := parseSemver(latest)
	if err != nil {
		return 0, fmt.Errorf("最新版本无效: %w", err)
	}
	return c.compare(l), nil
}

// isDevVersion reports whether the build does not carry a release SemVer.
func isDevVersion(v string) bool {
	_, err := parseSemver(v)
	return err != nil
}
