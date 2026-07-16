package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CodexSkill struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Scope   string `json:"scope"`
	Enabled bool   `json:"enabled"`
}

var listCodexSkills = probeCodexSkills
var listConfiguredCodexSkills = probeCodexSkillsWithArgs

func probeCodexSkills(cwd string) ([]CodexSkill, error) {
	return probeCodexSkillsWithArgs(cwd, nil)
}

func probeCodexSkillsWithArgs(cwd string, configArgs []string) ([]CodexSkill, error) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("find codex executable: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	arguments := []string{"app-server", "--stdio"}
	arguments = append(arguments, configArgs...)
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Dir = cwd
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = stdin.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()

	encoder := json.NewEncoder(stdin)
	requests := []any{
		map[string]any{
			"id":     1,
			"method": "initialize",
			"params": map[string]any{
				"clientInfo":   map[string]string{"name": "sm", "version": version},
				"capabilities": map[string]bool{"experimentalApi": true},
			},
		},
		map[string]any{"method": "initialized"},
		map[string]any{
			"id":     2,
			"method": "skills/list",
			"params": map[string]any{"cwds": []string{cwd}, "forceReload": true},
		},
	}
	for _, request := range requests {
		if err := encoder.Encode(request); err != nil {
			return nil, err
		}
	}

	decoder := json.NewDecoder(stdout)
	for {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result *struct {
				Data []struct {
					Cwd    string       `json:"cwd"`
					Skills []CodexSkill `json:"skills"`
					Errors []struct {
						Path    string `json:"path"`
						Message string `json:"message"`
					} `json:"errors"`
				} `json:"data"`
			} `json:"result"`
			Error json.RawMessage `json:"error"`
		}
		if err := decoder.Decode(&message); err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("codex skills/list timed out: %w", ctx.Err())
			}
			return nil, fmt.Errorf("read codex app-server response: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		if string(message.ID) != "2" {
			continue
		}
		if len(message.Error) != 0 && string(message.Error) != "null" {
			return nil, fmt.Errorf("codex skills/list failed: %s", message.Error)
		}
		if message.Result == nil || len(message.Result.Data) != 1 {
			return nil, fmt.Errorf("codex skills/list returned an invalid projection")
		}
		entry := message.Result.Data[0]
		if entry.Cwd != cwd {
			return nil, fmt.Errorf("codex skills/list returned cwd %q, want %q", entry.Cwd, cwd)
		}
		if len(entry.Errors) != 0 {
			return nil, fmt.Errorf("codex skill discovery error at %s: %s", entry.Errors[0].Path, entry.Errors[0].Message)
		}
		return entry.Skills, nil
	}
}

func codexAgentCommand(built Result, agentArgs []string) (Result, *exec.Cmd, error) {
	for _, argument := range agentArgs {
		if argument == "-c" || argument == "--config" || strings.HasPrefix(argument, "--config=") ||
			argument == "-p" || argument == "--profile" || strings.HasPrefix(argument, "--profile=") ||
			argument == "-C" || argument == "--cd" || strings.HasPrefix(argument, "--cd=") {
			return Result{}, nil, fmt.Errorf("agent argument %q can replace the verified Codex discovery context", argument)
		}
	}
	if err := validateActiveTarget(built.Target, built.Generation); err != nil {
		return Result{}, nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Result{}, nil, err
	}
	discovered, err := listCodexSkills(cwd)
	if err != nil {
		return Result{}, nil, err
	}
	config, err := codexDisableConfig(discovered, built.Generation)
	if err != nil {
		return Result{}, nil, err
	}
	configArgs := []string{"-c", config}
	projected, err := listConfiguredCodexSkills(cwd, configArgs)
	if err != nil {
		return Result{}, nil, err
	}
	if err := validateCodexClosure(projected, built.Generation); err != nil {
		return Result{}, nil, fmt.Errorf("codex execution profile did not close discovery: %w", err)
	}
	binary, err := findExecutable("codex")
	if err != nil {
		return Result{}, nil, fmt.Errorf("find codex executable: %w", err)
	}
	arguments := append(configArgs, agentArgs...)
	return built, exec.Command(binary, arguments...), nil
}

func codexDisableConfig(skills []CodexSkill, generation string) (string, error) {
	canonicalGeneration, err := filepath.EvalSymlinks(generation)
	if err != nil {
		return "", err
	}
	paths := make(map[string]struct{})
	for _, skill := range skills {
		if !skill.Enabled || skill.Scope == "system" {
			continue
		}
		canonical, err := filepath.EvalSymlinks(skill.Path)
		if err == nil && within(canonicalGeneration, canonical) {
			continue
		}
		paths[skill.Path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	entries := make([]string, 0, len(ordered))
	for _, path := range ordered {
		entries = append(entries, "{path="+strconv.Quote(path)+",enabled=false}")
	}
	return "skills.config=[" + strings.Join(entries, ",") + "]", nil
}

func validateActiveTarget(target, generation string) error {
	info, err := os.Lstat(target)
	if err != nil {
		return fmt.Errorf("inspect active target: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("active target is not an sm projection: %s", target)
	}
	actual, err := filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}
	want, err := filepath.EvalSymlinks(generation)
	if err != nil {
		return err
	}
	if actual != want {
		return fmt.Errorf("active target points to %s, want %s", actual, want)
	}
	return nil
}

func validateCodexClosure(skills []CodexSkill, generation string) error {
	canonicalGeneration, err := filepath.EvalSymlinks(generation)
	if err != nil {
		return fmt.Errorf("resolve generation for codex closure: %w", err)
	}
	expected := make(map[string]string)
	entries, err := os.ReadDir(canonicalGeneration)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(canonicalGeneration, entry.Name(), "SKILL.md")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			expected[path] = entry.Name()
		}
	}
	observed := make(map[string]struct{})
	var unmanaged []string
	for _, skill := range skills {
		if !skill.Enabled || skill.Scope == "system" {
			continue
		}
		path, err := filepath.EvalSymlinks(skill.Path)
		if err != nil {
			unmanaged = append(unmanaged, fmt.Sprintf("%s (%s)", skill.Path, skill.Name))
			continue
		}
		if !within(canonicalGeneration, path) {
			unmanaged = append(unmanaged, fmt.Sprintf("%s (%s)", skill.Path, skill.Name))
			continue
		}
		observed[path] = struct{}{}
	}
	var missing []string
	for path, name := range expected {
		if _, ok := observed[path]; !ok {
			missing = append(missing, fmt.Sprintf("%s (%s)", path, name))
		}
	}
	if len(unmanaged) == 0 && len(missing) == 0 {
		return nil
	}
	sort.Strings(unmanaged)
	sort.Strings(missing)
	parts := make([]string, 0, 2)
	if len(unmanaged) != 0 {
		parts = append(parts, "enabled skills outside the SSOT generation:\n  "+strings.Join(unmanaged, "\n  "))
	}
	if len(missing) != 0 {
		parts = append(parts, "authorized skills missing from Codex discovery:\n  "+strings.Join(missing, "\n  "))
	}
	return fmt.Errorf("codex discovery closure mismatch: %s", strings.Join(parts, "\n"))
}
