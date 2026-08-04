package skillmanager

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Distribution struct {
	Consumers []string `json:"consumers"`
	Platforms []string `json:"platforms"`
}

func loadDistribution(repo, commit, name string) (Distribution, error) {
	if err := validateID(name); err != nil {
		return Distribution{}, fmt.Errorf("invalid distribution: %w", err)
	}
	out, err := runGit(repo, "show", commit+":distributions/"+name+".json")
	if err != nil {
		return Distribution{}, fmt.Errorf("load distribution %q from commit: %w", name, err)
	}
	decoder := json.NewDecoder(strings.NewReader(out))
	decoder.DisallowUnknownFields()
	var distribution Distribution
	if err := decoder.Decode(&distribution); err != nil {
		return Distribution{}, fmt.Errorf("parse distribution %q: %w", name, err)
	}
	if err := validateDistribution(distribution); err != nil {
		return Distribution{}, fmt.Errorf("distribution %q: %w", name, err)
	}
	sort.Strings(distribution.Consumers)
	sort.Strings(distribution.Platforms)
	return distribution, nil
}

func validateDistribution(distribution Distribution) error {
	if len(distribution.Consumers) == 0 {
		return fmt.Errorf("at least one consumer is required")
	}
	if len(distribution.Platforms) == 0 {
		return fmt.Errorf("at least one platform is required")
	}
	if err := validateUniqueIDs("consumer", distribution.Consumers); err != nil {
		return err
	}
	seenPlatforms := make(map[string]struct{}, len(distribution.Platforms))
	for _, platform := range distribution.Platforms {
		if platform == "any" {
			return fmt.Errorf("target platform must be exact, not any")
		}
		if err := validateExecutablePlatform(platform); err != nil {
			return err
		}
		if _, exists := seenPlatforms[platform]; exists {
			return fmt.Errorf("duplicate platform %q", platform)
		}
		seenPlatforms[platform] = struct{}{}
	}
	return nil
}

func validateUniqueIDs(kind string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateID(value); err != nil {
			return fmt.Errorf("invalid %s: %w", kind, err)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s %q", kind, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}
