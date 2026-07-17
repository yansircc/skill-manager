package skillmanager

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func claudeAgentCommand(built Result, agentArgs []string) (Result, *exec.Cmd, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Result{}, nil, err
	}
	if err := validateClaudeProjectClosure(cwd); err != nil {
		return Result{}, nil, err
	}
	profile, err := prepareClaudeProfile(built.Generation, built.Consumer)
	if err != nil {
		return Result{}, nil, err
	}
	if err := validateClaudeProfileClosure(profile); err != nil {
		return Result{}, nil, err
	}
	binary, err := findExecutable("claude")
	if err != nil {
		return Result{}, nil, fmt.Errorf("find claude executable: %w", err)
	}
	arguments := []string{
		"--setting-sources", "",
		"--settings", `{"disableBundledSkills":true}`,
		"--plugin-dir", built.Generation,
	}
	arguments = append(arguments, agentArgs...)
	command := exec.Command(binary, arguments...)
	command.Env = replaceEnvironment(os.Environ(), "CLAUDE_CONFIG_DIR", profile)
	return built, command, nil
}

func prepareClaudeProfile(generation, consumer string) (string, error) {
	cache := filepath.Dir(filepath.Dir(generation))
	profile := filepath.Join(cache, "profiles", "claude", consumer)
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return "", err
	}
	return profile, nil
}

func validateClaudeProfileClosure(profile string) error {
	var paths []string
	for _, root := range []string{
		filepath.Join(profile, "skills"),
		filepath.Join(profile, "commands"),
		filepath.Join(profile, "plugins"),
	} {
		info, err := os.Stat(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("claude profile customization path is not a directory: %s", root)
		}
		err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && (entry.Name() == "SKILL.md" || strings.HasSuffix(entry.Name(), ".md") && filepath.Base(root) == "commands") {
				paths = append(paths, name)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	return fmt.Errorf("claude profile contains skills or plugins outside the SSOT generation:\n  %s", strings.Join(paths, "\n  "))
}

func validateClaudeProjectClosure(cwd string) error {
	root := cwd
	if output, err := runGit(cwd, "rev-parse", "--show-toplevel"); err == nil {
		root = strings.TrimSpace(output)
	}
	paths, err := findClaudeSkillSources(root)
	if err != nil {
		return err
	}
	managed := "/Library/Application Support/ClaudeCode/.claude"
	if info, err := os.Stat(managed); err == nil && info.IsDir() {
		managedPaths, err := findClaudeSkillSources(managed)
		if err != nil {
			return err
		}
		paths = append(paths, managedPaths...)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	return fmt.Errorf("claude discovery closure contains project or managed skills outside the SSOT generation:\n  %s", strings.Join(paths, "\n  "))
}

func findClaudeSkillSources(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		for index := 0; index+2 < len(parts); index++ {
			if parts[index] != ".claude" {
				continue
			}
			if parts[index+1] == "skills" && parts[len(parts)-1] == "SKILL.md" ||
				parts[index+1] == "commands" && strings.HasSuffix(parts[len(parts)-1], ".md") {
				paths = append(paths, name)
			}
		}
		return nil
	})
	return paths, err
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}
