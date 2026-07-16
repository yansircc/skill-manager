package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
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

func probeCodexSkills(cwd string) ([]CodexSkill, error) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("find codex executable: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "app-server", "--stdio")
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

func validateCodexClosure(skills []CodexSkill, generation string) error {
	canonicalGeneration, err := filepath.EvalSymlinks(generation)
	if err != nil {
		return fmt.Errorf("resolve generation for codex closure: %w", err)
	}
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
		}
	}
	if len(unmanaged) == 0 {
		return nil
	}
	sort.Strings(unmanaged)
	return fmt.Errorf("codex discovery closure contains enabled skills outside the SSOT generation:\n  %s", strings.Join(unmanaged, "\n  "))
}
