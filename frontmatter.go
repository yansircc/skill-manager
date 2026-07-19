package skillmanager

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type SkillMetadata struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Executables map[string]string `yaml:"executables"`
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
	for command, relative := range metadata.Executables {
		if err := validateID(command); err != nil {
			return SkillMetadata{}, fmt.Errorf("frontmatter executable %q: %w", command, err)
		}
		if relative == "" || filepath.IsAbs(relative) {
			return SkillMetadata{}, fmt.Errorf("frontmatter executable %q path must be relative", command)
		}
		clean := filepath.Clean(relative)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return SkillMetadata{}, fmt.Errorf("frontmatter executable %q path escapes the skill", command)
		}
		metadata.Executables[command] = clean
	}
	return metadata, nil
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
