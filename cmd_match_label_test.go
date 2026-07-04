package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLabelList(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", []string{}},
		{"major", []string{"major"}},
		{"major, minor, patch", []string{"major", "minor", "patch"}},
		{"major\nminor\npatch", []string{"major", "minor", "patch"}},
		{" major ,\n minor \n, patch ", []string{"major", "minor", "patch"}},
		{"major,,minor", []string{"major", "minor"}},
		{"major\r\nminor", []string{"major", "minor"}},
	}

	for _, tc := range tests {
		got := parseLabelList(tc.input)
		assert.Equal(t, tc.expected, got, "parseLabelList(%q)", tc.input)
	}
}

func TestFindMatching(t *testing.T) {
	tests := []struct {
		name          string
		labelNames    []string
		allowedLabels []string
		multiple      bool
		defaultMatch  string
		expected      []string
		wantErr       bool
	}{
		{
			name:          "single exact match",
			labelNames:    []string{"minor", "docs"},
			allowedLabels: []string{"major", "minor", "patch"},
			expected:      []string{"minor"},
		},
		{
			name:          "single mode fails when more than one matches",
			labelNames:    []string{"major", "minor"},
			allowedLabels: []string{"major", "minor", "patch"},
			wantErr:       true,
		},
		{
			name:          "single mode fails when none match",
			labelNames:    []string{"docs"},
			allowedLabels: []string{"major", "minor", "patch"},
			wantErr:       true,
		},
		{
			name:          "multiple mode matches many preserving PR order",
			labelNames:    []string{"b", "docs", "a"},
			allowedLabels: []string{"a", "b"},
			multiple:      true,
			expected:      []string{"b", "a"},
		},
		{
			name:          "multiple mode fails when none match",
			labelNames:    []string{"docs"},
			allowedLabels: []string{"a", "b"},
			multiple:      true,
			wantErr:       true,
		},
		{
			name:          "default used when no match (single)",
			labelNames:    []string{"docs"},
			allowedLabels: []string{"major", "minor"},
			defaultMatch:  "patch",
			expected:      []string{"patch"},
		},
		{
			name:          "default used when no match (multiple)",
			labelNames:    []string{"docs"},
			allowedLabels: []string{"a", "b"},
			multiple:      true,
			defaultMatch:  "a",
			expected:      []string{"a"},
		},
		{
			name:          "default ignored when a match exists",
			labelNames:    []string{"minor"},
			allowedLabels: []string{"major", "minor"},
			defaultMatch:  "patch",
			expected:      []string{"minor"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findMatching(tc.labelNames, tc.allowedLabels, tc.multiple, tc.defaultMatch)
			if tc.wantErr {
				require.Error(t, err, "expected error, got %v", got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expected, got)
		})
	}
}
