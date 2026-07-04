package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	matchAllowed         string
	matchAllowedMultiple string
	matchDefault         string
	matchLabels          string

	matchLabelCmd = &cobra.Command{
		Use:   "match-label",
		Short: "Match a PR's labels against an allowed list",
		RunE:  runMatchLabel,
	}
)

func init() {
	matchLabelCmd.Flags().StringVar(&matchAllowed, "allowed", "", "Comma- or newline-separated label names to match exactly one of")
	matchLabelCmd.Flags().StringVar(&matchAllowedMultiple, "allowed-multiple", "", "Comma- or newline-separated label names to match many of")
	matchLabelCmd.Flags().StringVar(&matchDefault, "default-match", "", "Label name to match if no matching labels are found")
	matchLabelCmd.Flags().StringVar(&matchLabels, "labels", "", "Comma- or newline-separated label names present on the PR")
	rootCmd.AddCommand(matchLabelCmd)
}

func runMatchLabel(cmd *cobra.Command, args []string) error {
	labelNames := parseLabelList(matchLabels)
	allowedLabels := parseLabelList(matchAllowed)
	allowedMultipleLabels := parseLabelList(matchAllowedMultiple)

	var matching []string
	var err error
	switch {
	case len(allowedLabels) > 0:
		matching, err = findMatching(labelNames, allowedLabels, false, matchDefault)
	case len(allowedMultipleLabels) > 0:
		matching, err = findMatching(labelNames, allowedMultipleLabels, true, matchDefault)
	default:
		return fmt.Errorf("you must provide either `allowed` or `allowed_multiple` as input")
	}
	if err != nil {
		return err
	}

	match := strings.Join(matching, ", ")
	fmt.Printf("match: %s\n", match)

	return writeGitHubOutput("match", match)
}

// parseLabelList splits a comma- or newline-separated string into trimmed,
// non-empty label names.
func parseLabelList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})

	labels := make([]string, 0, len(fields))
	for _, field := range fields {
		trimmed := strings.TrimSpace(field)
		if trimmed != "" {
			labels = append(labels, trimmed)
		}
	}
	return labels
}

// findMatching returns the labels present on the PR that appear in allowedLabels,
// preserving the order in which they appear on the PR. When no labels match and
// defaultMatch is non-empty, it returns the default. Otherwise it requires
// exactly one match (single mode) or at least one match (multiple mode).
func findMatching(labelNames, allowedLabels []string, isMultipleAllowed bool, defaultMatch string) ([]string, error) {
	allowed := make(map[string]struct{}, len(allowedLabels))
	for _, label := range allowedLabels {
		allowed[label] = struct{}{}
	}

	var matching []string
	for _, name := range labelNames {
		if _, ok := allowed[name]; ok {
			matching = append(matching, name)
		}
	}

	if len(matching) == 0 && defaultMatch != "" {
		return []string{defaultMatch}, nil
	}

	if isMultipleAllowed {
		if len(matching) < 1 {
			return nil, fmt.Errorf("could not find at least one of the appropriate labels on the PR")
		}
	} else {
		if len(matching) != 1 {
			return nil, fmt.Errorf("could not find exactly one of the appropriate labels on the PR")
		}
	}

	return matching, nil
}
