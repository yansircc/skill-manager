package skillmanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type claudeAdapter struct{}

func (claudeAdapter) Name() string     { return "claude" }
func (claudeAdapter) Persistent() bool { return false }

func (claudeAdapter) PrepareProjection(root, consumerName string, consumer Consumer) error {
	skillsRoot := filepath.Join(root, "skills")
	if err := os.Mkdir(skillsRoot, 0o755); err != nil {
		return err
	}
	for _, id := range consumer.Skills {
		if err := os.Rename(filepath.Join(root, id), filepath.Join(skillsRoot, id)); err != nil {
			return fmt.Errorf("shape Claude skill %q: %w", id, err)
		}
	}
	manifestRoot := filepath.Join(root, ".claude-plugin")
	if err := os.Mkdir(manifestRoot, 0o755); err != nil {
		return err
	}
	pluginName := strings.ToLower(consumerName)
	pluginName = strings.NewReplacer(".", "-", "_", "-").Replace(pluginName)
	manifest := map[string]string{
		"name":        "sm-" + pluginName,
		"version":     "1.0.0",
		"description": "Immutable sm projection for " + consumerName,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(manifestRoot, "plugin.json"), data, 0o644)
}

func (claudeAdapter) LaunchCommand(built Result, agentArgs []string) (Result, *exec.Cmd, error) {
	return claudeAgentCommand(built, agentArgs)
}

func (claudeAdapter) Verify(built Result, closed bool) (AdapterVerifyResult, error) {
	if !closed {
		return AdapterVerifyResult{}, fmt.Errorf("consumer %q uses ephemeral claude activation; use sm exec or verify --closed", built.Consumer)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return AdapterVerifyResult{}, err
	}
	if err := validateClaudeProjectClosure(cwd); err != nil {
		return AdapterVerifyResult{}, err
	}
	profile, err := prepareClaudeProfile(built.Generation, built.Consumer)
	if err != nil {
		return AdapterVerifyResult{}, err
	}
	if err := validateClaudeProfileClosure(profile); err != nil {
		return AdapterVerifyResult{}, err
	}
	records, err := claudeFilesystemDiscovery(cwd, profile)
	if err != nil {
		return AdapterVerifyResult{}, err
	}
	return AdapterVerifyResult{
		Evidence: &VerificationEvidence{
			Kind:      ProofKindFilesystemGuard,
			Discovery: records,
		},
	}, nil
}

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
	paths, err := claudeProjectSkillPaths(cwd)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	return fmt.Errorf("claude discovery closure contains project or managed skills outside the SSOT generation:\n  %s", strings.Join(paths, "\n  "))
}

func claudeFilesystemDiscovery(cwd, profile string) ([]DiscoveryRecord, error) {
	var records []DiscoveryRecord
	projectPaths, err := claudeProjectSkillPaths(cwd)
	if err != nil {
		return nil, err
	}
	for _, path := range projectPaths {
		records = append(records, DiscoveryRecord{
			Name:    filepath.Base(filepath.Dir(path)),
			Path:    path,
			Source:  "project",
			Enabled: true,
		})
	}
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
			return nil, err
		}
		if !info.IsDir() {
			continue
		}
		source := filepath.Base(root)
		err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Name() == "SKILL.md" || strings.HasSuffix(entry.Name(), ".md") && source == "commands" {
				records = append(records, DiscoveryRecord{
					Name:    filepath.Base(filepath.Dir(name)),
					Path:    name,
					Source:  "profile",
					Scope:   source,
					Enabled: true,
				})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Path == records[j].Path {
			return records[i].Name < records[j].Name
		}
		return records[i].Path < records[j].Path
	})
	return records, nil
}

func claudeProjectSkillPaths(cwd string) ([]string, error) {
	root := cwd
	if output, err := runGit(cwd, "rev-parse", "--show-toplevel"); err == nil {
		root = strings.TrimSpace(output)
	}
	paths, err := findClaudeSkillSources(root)
	if err != nil {
		return nil, err
	}
	managed := "/Library/Application Support/ClaudeCode/.claude"
	if info, err := os.Stat(managed); err == nil && info.IsDir() {
		managedPaths, err := findClaudeSkillSources(managed)
		if err != nil {
			return nil, err
		}
		paths = append(paths, managedPaths...)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return paths, nil
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
