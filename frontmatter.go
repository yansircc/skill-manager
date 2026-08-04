package skillmanager

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type SkillMetadata struct {
	Name        string                           `yaml:"name"`
	Description string                           `yaml:"description"`
	Executables map[string]ExecutableDeclaration `yaml:"executables"`
}

type ExecutableDeclaration map[string]string

func (declaration *ExecutableDeclaration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("must map platforms to paths; use any for a portable executable")
	}
	var paths map[string]string
	if err := node.Decode(&paths); err != nil {
		return err
	}
	*declaration = paths
	return nil
}

func readSkillMetadata(root string) (SkillMetadata, error) {
	data, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return SkillMetadata{}, fmt.Errorf("read SKILL.md: %w", err)
	}
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) < 3 || string(bytes.TrimSpace(lines[0])) != "---" {
		return SkillMetadata{}, fmt.Errorf("SKILL.md has no YAML frontmatter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if string(bytes.TrimSpace(lines[i])) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return SkillMetadata{}, fmt.Errorf("SKILL.md frontmatter is not closed")
	}
	var metadata SkillMetadata
	decoder := yaml.NewDecoder(bytes.NewReader(bytes.Join(lines[1:end], []byte("\n"))))
	if err := decoder.Decode(&metadata); err != nil {
		return SkillMetadata{}, fmt.Errorf("parse SKILL.md frontmatter: %w", err)
	}
	if err := validateID(metadata.Name); err != nil {
		return SkillMetadata{}, fmt.Errorf("frontmatter name: %w", err)
	}
	for command, declaration := range metadata.Executables {
		if err := validateID(command); err != nil {
			return SkillMetadata{}, fmt.Errorf("frontmatter executable %q: %w", command, err)
		}
		if len(declaration) == 0 {
			return SkillMetadata{}, fmt.Errorf("frontmatter executable %q has no platform paths", command)
		}
		for platform, relative := range declaration {
			if err := validateExecutablePlatform(platform); err != nil {
				return SkillMetadata{}, fmt.Errorf("frontmatter executable %q: %w", command, err)
			}
			if relative == "" || filepath.IsAbs(relative) {
				return SkillMetadata{}, fmt.Errorf("frontmatter executable %q platform %q path must be relative", command, platform)
			}
			clean := filepath.Clean(relative)
			if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return SkillMetadata{}, fmt.Errorf("frontmatter executable %q platform %q path escapes the skill", command, platform)
			}
			declaration[platform] = clean
		}
	}
	return metadata, nil
}

func validateExecutablePlatform(platform string) error {
	if platform == "any" {
		return nil
	}
	parts := strings.Split(platform, "-")
	if len(parts) != 2 {
		return fmt.Errorf("invalid executable platform %q; want GOOS-GOARCH or any", platform)
	}
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf("invalid executable platform %q; want GOOS-GOARCH or any", platform)
		}
		for _, character := range part {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')) {
				return fmt.Errorf("invalid executable platform %q; want GOOS-GOARCH or any", platform)
			}
		}
	}
	return nil
}

func resolveExecutablePath(declaration ExecutableDeclaration, goos, goarch string) (string, error) {
	platform := goos + "-" + goarch
	if relative, ok := declaration[platform]; ok {
		return relative, nil
	}
	if relative, ok := declaration["any"]; ok {
		return relative, nil
	}
	available := make([]string, 0, len(declaration))
	for candidate := range declaration {
		available = append(available, candidate)
	}
	sort.Strings(available)
	return "", fmt.Errorf("has no artifact for platform %s (available: %s)", platform, strings.Join(available, ", "))
}

func readOptionalSkillMetadata(root string) (SkillMetadata, bool, error) {
	data, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return SkillMetadata{}, false, err
	}
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) == 0 || string(bytes.TrimSpace(lines[0])) != "---" {
		return SkillMetadata{}, false, nil
	}
	metadata, err := readSkillMetadata(root)
	return metadata, err == nil, err
}
