package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/spf13/cobra"
)

var (
	from          string
	labelPrefix   string
	releasePrefix string
	preid         string

	bumpCmd = &cobra.Command{
		Use:   "bump",
		Short: "Determine the next semver release",
		RunE:  runBump,
	}
)

func init() {
	bumpCmd.Flags().StringVar(&from, "from", "", "Label (major, minor, patch, etc.) or version number")
	bumpCmd.Flags().StringVar(&labelPrefix, "label-prefix", "", "Label prefix")
	bumpCmd.Flags().StringVar(&releasePrefix, "release-prefix", "v", "Release prefix")
	bumpCmd.Flags().StringVar(&preid, "preid", "", "Pre-release id")
	rootCmd.AddCommand(bumpCmd)
}

func runBump(cmd *cobra.Command, args []string) error {
	if from == "" {
		return fmt.Errorf("--from is required")
	}

	var currentVersion *semver.Version
	var bumpType string

	// check if `from` is a label
	label := from
	if labelPrefix != "" && strings.HasPrefix(from, labelPrefix) {
		label = strings.TrimPrefix(from, labelPrefix)
	}

	switch label {
	case "major", "minor", "patch", "prerelease", "premajor", "preminor", "prepatch":
		bumpType = label
		latest, err := getLatestTagGoGit(releasePrefix)
		if err != nil {
			currentVersion, _ = semver.NewVersion("0.0.0")
		} else {
			currentVersion, err = semver.NewVersion(latest)
			if err != nil {
				return fmt.Errorf("failed to parse latest tag %s: %w", latest, err)
			}
		}
	default:
		v, err := semver.NewVersion(from)
		if err != nil {
			return fmt.Errorf("invalid version or label: %s", from)
		}
		currentVersion = v
	}

	var nextVersion semver.Version
	if bumpType != "" {
		switch bumpType {
		case "major":
			nextVersion = currentVersion.IncMajor()
		case "minor":
			nextVersion = currentVersion.IncMinor()
		case "patch":
			nextVersion = currentVersion.IncPatch()
		case "premajor":
			v := currentVersion.IncMajor()
			if preid != "" {
				nextVersion, _ = v.SetPrerelease(getPreid(preid, 0))
			} else {
				nextVersion, _ = v.SetPrerelease("0")
			}
		case "preminor":
			v := currentVersion.IncMinor()
			if preid != "" {
				nextVersion, _ = v.SetPrerelease(getPreid(preid, 0))
			} else {
				nextVersion, _ = v.SetPrerelease("0")
			}
		case "prepatch":
			v := currentVersion.IncPatch()
			if preid != "" {
				nextVersion, _ = v.SetPrerelease(getPreid(preid, 0))
			} else {
				nextVersion, _ = v.SetPrerelease("0")
			}
		case "prerelease":
			if currentVersion.Prerelease() != "" {
				// If current is already a prerelease, increment it
				nextVersion = incPrerelease(*currentVersion)
				// But if preid is specified and different, we might want to reset it
				if preid != "" && !strings.HasPrefix(nextVersion.Prerelease(), preid+".") && nextVersion.Prerelease() != preid {
					nextVersion, _ = currentVersion.SetPrerelease(getPreid(preid, 0))
				}
			} else {
				v := currentVersion.IncPatch()
				if preid != "" {
					nextVersion, _ = v.SetPrerelease(getPreid(preid, 0))
				} else {
					nextVersion, _ = v.SetPrerelease("0")
				}
			}
		}
	} else {
		nextVersion = *currentVersion
	}

	versionStr := nextVersion.String()
	tagStr := releasePrefix + versionStr

	fmt.Printf("next-version: %s\n", versionStr)
	fmt.Printf("next-tag: %s\n", tagStr)

	githubOutput := os.Getenv("GITHUB_OUTPUT")
	if githubOutput != "" {
		f, err := os.OpenFile(githubOutput, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(f, "next-version=%s\n", versionStr)
		fmt.Fprintf(f, "next-tag=%s\n", tagStr)
	}

	return nil
}

func getPreid(id string, defaultValue int) string {
	if id == "" {
		return fmt.Sprintf("%d", defaultValue)
	}
	return fmt.Sprintf("%s.%d", id, defaultValue)
}

func incPrerelease(v semver.Version) semver.Version {
	pre := v.Prerelease()
	if pre == "" {
		newV, _ := v.SetPrerelease("0")
		return newV
	}

	parts := strings.Split(pre, ".")
	last := parts[len(parts)-1]
	if num, err := strconv.Atoi(last); err == nil {
		parts[len(parts)-1] = strconv.Itoa(num + 1)
	} else {
		parts = append(parts, "0")
	}

	newV, _ := v.SetPrerelease(strings.Join(parts, "."))
	return newV
}

func getLatestTagGoGit(prefix string) (string, error) {
	r, err := git.PlainOpen(".")
	if err != nil {
		return "", err
	}

	iter, err := r.Tags()
	if err != nil {
		return "", err
	}

	var latest *semver.Version
	var latestTag string

	err = iter.ForEach(func(t *plumbing.Reference) error {
		tagName := t.Name().Short()
		if strings.HasPrefix(tagName, prefix) {
			vStr := strings.TrimPrefix(tagName, prefix)
			v, err := semver.NewVersion(vStr)
			if err == nil {
				if latest == nil || v.GreaterThan(latest) {
					latest = v
					latestTag = tagName
				}
			}
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	if latestTag == "" {
		return "", fmt.Errorf("no tags found")
	}

	return latestTag, nil
}
