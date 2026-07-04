package main

import (
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"
)

func TestValidateVersion(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"1.0.0", true},
		{"v1.2.3", true},
		{"1.2.3-alpha.0", true},
		{"invalid", false},
		{"1.0", true},
		{"v1.0", true},
	}

	for _, tc := range tests {
		_, err := semver.NewVersion(tc.input)
		assert.Equal(t, tc.valid, err == nil, "ValidateVersion(%s) valid", tc.input)
	}
}
