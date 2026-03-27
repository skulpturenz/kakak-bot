package main

import (
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func TestIncPrerelease(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1.0.0-alpha", "1.0.0-alpha.0"},
		{"1.0.0-alpha.0", "1.0.0-alpha.1"},
		{"1.0.0-0", "1.0.0-1"},
		{"1.0.0", "1.0.0-0"},
		{"1.2.3-beta.9", "1.2.3-beta.10"},
		{"1.2.3-rc.1.test", "1.2.3-rc.1.test.0"},
	}

	for _, tc := range tests {
		v, _ := semver.NewVersion(tc.input)
		next := incPrerelease(*v)
		if next.String() != tc.expected {
			t.Errorf("incPrerelease(%s) = %s; want %s", tc.input, next.String(), tc.expected)
		}
	}
}

func TestGetPreid(t *testing.T) {
	tests := []struct {
		id       string
		def      int
		expected string
	}{
		{"alpha", 0, "alpha.0"},
		{"", 0, "0"},
		{"beta", 1, "beta.1"},
	}

	for _, tc := range tests {
		res := getPreid(tc.id, tc.def)
		if res != tc.expected {
			t.Errorf("getPreid(%s, %d) = %s; want %s", tc.id, tc.def, res, tc.expected)
		}
	}
}

func TestBumpLogic(t *testing.T) {
	base, _ := semver.NewVersion("1.0.0")

	tests := []struct {
		bumpType string
		preid    string
		expected string
	}{
		{"major", "", "2.0.0"},
		{"minor", "", "1.1.0"},
		{"patch", "", "1.0.1"},
		{"premajor", "alpha", "2.0.0-alpha.0"},
		{"preminor", "beta", "1.1.0-beta.0"},
		{"prepatch", "rc", "1.0.1-rc.0"},
		{"premajor", "", "2.0.0-0"},
	}

	for _, tc := range tests {
		var next semver.Version
		switch tc.bumpType {
		case "major":
			next = base.IncMajor()
		case "minor":
			next = base.IncMinor()
		case "patch":
			next = base.IncPatch()
		case "premajor":
			v := base.IncMajor()
			if tc.preid != "" {
				next, _ = v.SetPrerelease(getPreid(tc.preid, 0))
			} else {
				next, _ = v.SetPrerelease("0")
			}
		case "preminor":
			v := base.IncMinor()
			if tc.preid != "" {
				next, _ = v.SetPrerelease(getPreid(tc.preid, 0))
			} else {
				next, _ = v.SetPrerelease("0")
			}
		case "prepatch":
			v := base.IncPatch()
			if tc.preid != "" {
				next, _ = v.SetPrerelease(getPreid(tc.preid, 0))
			} else {
				next, _ = v.SetPrerelease("0")
			}
		}

		if next.String() != tc.expected {
			t.Errorf("Bump %s (preid: %s) from %s = %s; want %s", tc.bumpType, tc.preid, base.String(), next.String(), tc.expected)
		}
	}
}

func TestPrereleaseBumpLogic(t *testing.T) {
	tests := []struct {
		current  string
		preid    string
		expected string
	}{
		{"1.0.0-alpha.0", "alpha", "1.0.0-alpha.1"},
		{"1.0.0-alpha.0", "beta", "1.0.0-beta.0"},
		{"1.0.0", "alpha", "1.0.1-alpha.0"},
		{"1.0.0-0", "", "1.0.0-1"},
	}

	for _, tc := range tests {
		currentVersion, _ := semver.NewVersion(tc.current)
		var nextVersion semver.Version

		if currentVersion.Prerelease() != "" {
			nextVersion = incPrerelease(*currentVersion)
			if tc.preid != "" && !strings.HasPrefix(nextVersion.Prerelease(), tc.preid+".") && nextVersion.Prerelease() != tc.preid {
				nextVersion, _ = currentVersion.SetPrerelease(getPreid(tc.preid, 0))
			}
		} else {
			v := currentVersion.IncPatch()
			if tc.preid != "" {
				nextVersion, _ = v.SetPrerelease(getPreid(tc.preid, 0))
			} else {
				nextVersion, _ = v.SetPrerelease("0")
			}
		}

		if nextVersion.String() != tc.expected {
			t.Errorf("Prerelease bump (preid: %s) from %s = %s; want %s", tc.preid, tc.current, nextVersion.String(), tc.expected)
		}
	}
}
