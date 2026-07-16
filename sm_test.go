package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildApplyVerifyUsesCommittedState(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "codex-skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })

	writeSkill(t, repo, "alpha", "committed")
	writeConsumer(t, repo, "codex.global", Consumer{
		Adapter: "directory",
		Target:  target,
		Skills:  []string{"alpha"},
	})
	commit := commitAll(t, repo, "initial")

	built, err := Build(repo, "HEAD", "codex.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	if built.Commit != commit {
		t.Fatalf("commit = %q, want %q", built.Commit, commit)
	}
	assertFileContains(t, filepath.Join(built.Generation, "alpha", "SKILL.md"), "committed")

	// The worktree is proposed state. HEAD remains the published input.
	writeFile(t, filepath.Join(repo, "skills", "alpha", "SKILL.md"), "uncommitted")
	second, err := Build(repo, "HEAD", "codex.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != built.Generation {
		t.Fatalf("same input produced different generations: %s != %s", second.Generation, built.Generation)
	}
	assertFileContains(t, filepath.Join(second.Generation, "alpha", "SKILL.md"), "committed")

	applied, err := Apply(repo, "HEAD", "codex.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Generation != built.Generation {
		t.Fatalf("apply activated %s, want %s", applied.Generation, built.Generation)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("target is not a symlink: %s", target)
	}
	if _, err := Verify(repo, "HEAD", "codex.global", cache); err != nil {
		t.Fatal(err)
	}

	// Idempotence: applying the same function value is a no-op semantically.
	if _, err := Apply(repo, "HEAD", "codex.global", cache); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyChangeReplacesWholeProjection(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })

	writeSkill(t, repo, "alpha", "alpha")
	writeSkill(t, repo, "beta", "beta")
	writeConsumer(t, repo, "claude.global", Consumer{
		Adapter: "directory",
		Target:  target,
		Skills:  []string{"alpha", "beta"},
	})
	commitAll(t, repo, "both skills")
	first, err := Apply(repo, "HEAD", "claude.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "beta", "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	writeConsumer(t, repo, "claude.global", Consumer{
		Adapter: "directory",
		Target:  target,
		Skills:  []string{"alpha"},
	})
	commitAll(t, repo, "revoke beta")
	second, err := Apply(repo, "HEAD", "claude.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation == second.Generation {
		t.Fatal("policy change reused the old generation")
	}
	if _, err := os.Stat(filepath.Join(target, "beta")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revoked skill remains discoverable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "alpha", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(repo, "HEAD", "claude.global", cache); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyAuthorizationBuildsEmptyProjection(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "not-authorized", "secret")
	writeConsumer(t, repo, "pi.empty", Consumer{Adapter: "directory", Target: target})
	commitAll(t, repo, "empty policy")

	result, err := Apply(repo, "HEAD", "pi.empty", cache)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(result.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != markerName {
		t.Fatalf("empty policy projected unexpected entries: %#v", entries)
	}
}

func TestVerifyRejectsProjectionDrift(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "original")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "initial")
	result, err := Apply(repo, "HEAD", "codex.global", cache)
	if err != nil {
		t.Fatal(err)
	}

	skillFile := filepath.Join(result.Generation, "alpha", "SKILL.md")
	if err := os.Chmod(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(skillFile, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, skillFile, "drift")
	if _, err := Verify(repo, "HEAD", "codex.global", cache); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("Verify error = %v, want projection drift", err)
	}
}

func TestApplyRefusesUnmanagedTarget(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "initial")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(target, "unmanaged"), "do not delete")

	if _, err := Apply(repo, "HEAD", "codex.global", cache); err == nil || !strings.Contains(err.Error(), "unmanaged") {
		t.Fatalf("Apply error = %v, want unmanaged target refusal", err)
	}
}

func TestBuildFailsClosedOnInvalidCatalog(t *testing.T) {
	t.Run("missing authorized skill", func(t *testing.T) {
		repo := newTestRepository(t)
		cache := filepath.Join(t.TempDir(), "cache")
		t.Cleanup(func() { _ = makeWritable(cache) })
		writeConsumer(t, repo, "codex.global", Consumer{
			Adapter: "directory",
			Target:  filepath.Join(t.TempDir(), "skills"),
			Skills:  []string{"missing"},
		})
		commitAll(t, repo, "invalid policy")
		if _, err := Build(repo, "HEAD", "codex.global", cache); err == nil {
			t.Fatal("Build accepted an authorized skill absent from the committed catalog")
		}
	})

	t.Run("symlink in canonical skill", func(t *testing.T) {
		repo := newTestRepository(t)
		cache := filepath.Join(t.TempDir(), "cache")
		t.Cleanup(func() { _ = makeWritable(cache) })
		writeSkill(t, repo, "alpha", "alpha")
		if err := os.Symlink("SKILL.md", filepath.Join(repo, "skills", "alpha", "alias")); err != nil {
			t.Fatal(err)
		}
		writeConsumer(t, repo, "codex.global", Consumer{
			Adapter: "directory",
			Target:  filepath.Join(t.TempDir(), "skills"),
			Skills:  []string{"alpha"},
		})
		commitAll(t, repo, "symlink")
		if _, err := Build(repo, "HEAD", "codex.global", cache); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("Build error = %v, want unsupported archive entry", err)
		}
	})
}

func TestConsumerRequiresStableTarget(t *testing.T) {
	err := validateConsumer(Consumer{Adapter: "directory", Target: "relative/skills"})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("validateConsumer error = %v, want absolute target failure", err)
	}
}

func TestPiAgentCommandClosesDiscovery(t *testing.T) {
	repo := newTestRepository(t)
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{"alpha"}})
	commitAll(t, repo, "pi consumer")
	originalExecutable := findExecutable
	findExecutable = func(string) (string, error) { return "/usr/bin/true", nil }
	t.Cleanup(func() { findExecutable = originalExecutable })

	result, command, err := AgentCommand(repo, "HEAD", "pi.global", cache, []string{"--print", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{command.Path, "--no-extensions", "--no-skills", "--skill", result.Generation, "--print", "hello"}
	if strings.Join(command.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command args = %#v, want %#v", command.Args, want)
	}
	if result.Target != "" {
		t.Fatalf("ephemeral consumer has target %q", result.Target)
	}
	if _, err := Apply(repo, "HEAD", "pi.global", cache); err == nil || !strings.Contains(err.Error(), "sm exec") {
		t.Fatalf("Apply error = %v, want exec boundary", err)
	}
	if _, err := Verify(repo, "HEAD", "pi.global", cache); err == nil || !strings.Contains(err.Error(), "sm exec") {
		t.Fatalf("Verify error = %v, want exec boundary", err)
	}
}

func TestPiAgentCommandRejectsSkillExpansion(t *testing.T) {
	for _, argument := range []string{"--skill", "--skill=/tmp/other", "--extension", "--extension=/tmp/plugin", "-e"} {
		if _, _, err := AgentCommand(".", "HEAD", "pi.global", "", []string{argument}); err == nil || !strings.Contains(err.Error(), "expand") {
			t.Fatalf("AgentCommand(%q) error = %v, want expansion refusal", argument, err)
		}
	}
}

func TestCodexVerifyUsesRuntimeDiscoveryProjection(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "codex", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "codex consumer")
	result, err := Apply(repo, "HEAD", "codex.global", cache)
	if err != nil {
		t.Fatal(err)
	}

	original := listCodexSkills
	t.Cleanup(func() { listCodexSkills = original })
	listCodexSkills = func(string) ([]CodexSkill, error) {
		return []CodexSkill{
			{Name: "alpha", Path: filepath.Join(result.Generation, "alpha", "SKILL.md"), Scope: "user", Enabled: true},
			{Name: "skill-creator", Path: "/system/skill-creator/SKILL.md", Scope: "system", Enabled: true},
		}, nil
	}
	if _, err := Verify(repo, "HEAD", "codex.global", cache); err != nil {
		t.Fatal(err)
	}

	listCodexSkills = func(string) ([]CodexSkill, error) {
		return []CodexSkill{{Name: "foreign", Path: filepath.Join(t.TempDir(), "foreign", "SKILL.md"), Scope: "repo", Enabled: true}}, nil
	}
	if _, err := Verify(repo, "HEAD", "codex.global", cache); err == nil || !strings.Contains(err.Error(), "outside the SSOT") {
		t.Fatalf("Verify error = %v, want Codex closure failure", err)
	}
}

func TestClaudeAgentCommandUsesIsolatedPluginProjection(t *testing.T) {
	repo := newTestRepository(t)
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "claude.global", Consumer{Adapter: "claude", Skills: []string{"alpha"}})
	commitAll(t, repo, "claude consumer")
	originalExecutable := findExecutable
	findExecutable = func(string) (string, error) { return "/usr/bin/true", nil }
	t.Cleanup(func() { findExecutable = originalExecutable })

	result, command, err := AgentCommand(repo, "HEAD", "claude.global", cache, []string{"--print", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(result.Generation, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(result.Generation, "skills", "alpha", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	wantPrefix := []string{command.Path, "--setting-sources", "", "--settings", `{"disableBundledSkills":true}`, "--plugin-dir", result.Generation}
	if len(command.Args) < len(wantPrefix) || strings.Join(command.Args[:len(wantPrefix)], "\x00") != strings.Join(wantPrefix, "\x00") {
		t.Fatalf("command args = %#v, want prefix %#v", command.Args, wantPrefix)
	}
	if !environmentContains(command.Env, "CLAUDE_CONFIG_DIR=") {
		t.Fatalf("command environment lacks isolated CLAUDE_CONFIG_DIR")
	}
}

func TestClaudeProjectClosureRejectsNestedSkill(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "packages", "web", ".claude", "skills", "deploy", "SKILL.md"), "deploy")
	paths, err := findClaudeSkillSources(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "SKILL.md") {
		t.Fatalf("Claude sources = %#v", paths)
	}
}

func environmentContains(environment []string, prefix string) bool {
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func TestAdoptMovesCanonicalSkill(t *testing.T) {
	repo := newTestRepository(t)
	external := t.TempDir()
	source := filepath.Join(external, "my-skill")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "SKILL.md"), "canonical")

	destination, err := Adopt(repo, source, "")
	if err != nil {
		t.Fatal(err)
	}
	canonicalRepo, err := repositoryRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if destination != filepath.Join(canonicalRepo, "skills", "my-skill") {
		t.Fatalf("destination = %s", destination)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists after adoption: %v", err)
	}
	assertFileContains(t, filepath.Join(destination, "SKILL.md"), "canonical")
}

func TestScanDiscoversCandidatesWithoutGrantingOwnership(t *testing.T) {
	repo := newTestRepository(t)
	sources := t.TempDir()
	writeFile(t, filepath.Join(sources, "one", "SKILL.md"), "one")
	writeFile(t, filepath.Join(sources, "nested", "two", "SKILL.md"), "two")

	candidates, err := Scan(repo, []string{sources, filepath.Join(sources, "nested")})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v", candidates)
	}
	identities := map[string]bool{candidates[0].ID: true, candidates[1].ID: true}
	if !identities["one"] || !identities["two"] {
		t.Fatalf("candidates = %#v", candidates)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate.Path); err != nil {
			t.Fatalf("scan mutated candidate %s: %v", candidate.Path, err)
		}
	}
}

func TestScanRejectsDuplicateIdentity(t *testing.T) {
	repo := newTestRepository(t)
	first := t.TempDir()
	second := t.TempDir()
	writeFile(t, filepath.Join(first, "same", "SKILL.md"), "first")
	writeFile(t, filepath.Join(second, "same", "SKILL.md"), "second")
	if _, err := Scan(repo, []string{first, second}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("Scan error = %v, want duplicate identity", err)
	}
}

func TestNestedSkillIsInvalidCanonicalPackage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "SKILL.md"), "outer")
	writeFile(t, filepath.Join(root, "nested", "SKILL.md"), "inner")
	if err := validateCanonicalSkill(root); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("validateCanonicalSkill error = %v, want nested skill failure", err)
	}
}

func newTestRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if _, err := Init(repo); err != nil {
		t.Fatal(err)
	}
	return repo
}

func writeSkill(t *testing.T, repo, id, content string) {
	t.Helper()
	directory := filepath.Join(repo, "skills", id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "SKILL.md"), content)
}

func writeConsumer(t *testing.T, repo, name string, consumer Consumer) {
	t.Helper()
	data, err := json.MarshalIndent(consumer, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	writeFile(t, filepath.Join(repo, "consumers", name+".json"), string(data))
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, repo, message string) string {
	t.Helper()
	if _, err := runGit(repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repo, "-c", "user.name=sm-test", "-c", "user.email=sm-test@example.com", "commit", "-m", message); err != nil {
		t.Fatal(err)
	}
	commit, err := resolveCommit(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return commit
}

func assertFileContains(t *testing.T, name, want string) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", name, data, want)
	}
}
