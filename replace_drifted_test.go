package skillmanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceDriftedSuccessPreservesOldAndVerifies(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	evidence := filepath.Join(t.TempDir(), "evidence")
	t.Cleanup(func() { _ = makeWritable(cache) })

	writeSkill(t, repo, "alpha", "original")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commit := commitAll(t, repo, "initial")
	applied, err := Apply(repo, "HEAD", "codex.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := applied.Generation
	oldMarker, err := readGenerationMarker(oldGeneration)
	if err != nil {
		t.Fatal(err)
	}

	driftProjectionContent(t, oldGeneration, "alpha", "drifted")
	if _, err := Apply(repo, "HEAD", "codex.global", cache); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("Apply error = %v, want drift refusal", err)
	}
	if _, err := Verify(repo, "HEAD", "codex.global", cache); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("Verify error = %v, want drift", err)
	}

	replaced, err := ReplaceDrifted(repo, "HEAD", "codex.global", cache, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Commit != commit {
		t.Fatalf("commit = %q, want %q", replaced.Commit, commit)
	}
	if replaced.Evidence.PreservedGeneration == "" || replaced.Evidence.PreservedGeneration == replaced.Generation {
		t.Fatalf("preserved generation = %q, new = %q", replaced.Evidence.PreservedGeneration, replaced.Generation)
	}
	if _, err := os.Lstat(replaced.Evidence.PreservedGeneration); err != nil {
		t.Fatalf("preserved generation missing: %v", err)
	}
	assertFileContains(t, filepath.Join(replaced.Evidence.PreservedGeneration, "alpha", "SKILL.md"), "drifted")
	assertFileContains(t, filepath.Join(replaced.Generation, "alpha", "SKILL.md"), "original")

	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(replaced.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("active target = %s, want %s", resolved, want)
	}
	if _, err := Verify(repo, "HEAD", "codex.global", cache); err != nil {
		t.Fatal(err)
	}
	if replaced.Verification.Generation != replaced.Generation {
		t.Fatalf("verification generation = %s, want %s", replaced.Verification.Generation, replaced.Generation)
	}

	data, err := os.ReadFile(filepath.Join(evidence, replaceDriftedEvidenceName))
	if err != nil {
		t.Fatal(err)
	}
	var recorded ReplaceDriftedEvidence
	if err := json.Unmarshal(data, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Schema != 1 || recorded.Consumer != "codex.global" || recorded.RepoCommit != commit {
		t.Fatalf("evidence identity = %#v", recorded)
	}
	if recorded.OldExpectedTreeHash != oldMarker.TreeHash {
		t.Fatalf("expected tree hash = %q, want %q", recorded.OldExpectedTreeHash, oldMarker.TreeHash)
	}
	if recorded.OldActualTreeHash == recorded.OldExpectedTreeHash {
		t.Fatal("evidence did not record content hash drift")
	}
	if recorded.OldMarker.TreeHash != oldMarker.TreeHash {
		t.Fatalf("old marker = %#v", recorded.OldMarker)
	}
	if recorded.NewGeneration != replaced.Generation || recorded.NewMarker.TreeHash == "" {
		t.Fatalf("new generation evidence = %#v", recorded)
	}
	if recorded.Timestamp == "" || recorded.OldTarget != target {
		t.Fatalf("evidence metadata = %#v", recorded)
	}
	foundModified := false
	for _, change := range recorded.Changes {
		if change.Path == "alpha/SKILL.md" && change.Type == "modified" {
			foundModified = true
		}
		if strings.Contains(change.Path, "SKILL.md") && change.Type == "modified" {
			// Evidence must not embed file contents; only path/type metadata.
			if change.Path != "alpha/SKILL.md" {
				t.Fatalf("unexpected change path %q", change.Path)
			}
		}
	}
	if !foundModified {
		t.Fatalf("changes = %#v, want modified alpha/SKILL.md", recorded.Changes)
	}
	if strings.Contains(string(data), "drifted") || strings.Contains(string(data), "original") {
		t.Fatalf("evidence leaked skill contents: %s", data)
	}
}

func TestReplaceDriftedRejectsOrdinaryDirectory(t *testing.T) {
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
	writeFile(t, filepath.Join(target, "unmanaged"), "no")

	_, err := ReplaceDrifted(repo, "HEAD", "codex.global", cache, filepath.Join(t.TempDir(), "evidence"))
	if err == nil || !strings.Contains(err.Error(), "ordinary directory") {
		t.Fatalf("ReplaceDrifted error = %v, want ordinary directory refusal", err)
	}
}

func TestReplaceDriftedRejectsNonSMSymlink(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "initial")
	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(foreign, "SKILL.md"), "foreign")
	if err := os.Symlink(foreign, target); err != nil {
		t.Fatal(err)
	}

	_, err := ReplaceDrifted(repo, "HEAD", "codex.global", cache, filepath.Join(t.TempDir(), "evidence"))
	if err == nil || !strings.Contains(err.Error(), "not owned by sm") {
		t.Fatalf("ReplaceDrifted error = %v, want non-sm symlink refusal", err)
	}
}

func TestReplaceDriftedRejectsWrongConsumer(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "codex")
	if _, err := Apply(repo, "HEAD", "codex.global", cache); err != nil {
		t.Fatal(err)
	}
	writeConsumer(t, repo, "claude.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "claude")

	_, err := ReplaceDrifted(repo, "HEAD", "claude.global", cache, filepath.Join(t.TempDir(), "evidence"))
	if err == nil || !strings.Contains(err.Error(), "not owned by sm") {
		t.Fatalf("ReplaceDrifted error = %v, want wrong consumer refusal", err)
	}
}

func TestReplaceDriftedRejectsOrdinaryFile(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "initial")
	writeFile(t, target, "not a projection")

	_, err := ReplaceDrifted(repo, "HEAD", "codex.global", cache, filepath.Join(t.TempDir(), "evidence"))
	if err == nil || !strings.Contains(err.Error(), "not an sm projection symlink") {
		t.Fatalf("ReplaceDrifted error = %v, want ordinary file refusal", err)
	}
}

func TestReplaceDriftedRejectsInvalidMarker(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "initial")
	generation := filepath.Join(t.TempDir(), "fake-generation")
	if err := os.MkdirAll(generation, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(generation, markerName), "{not-json")
	if err := os.Symlink(generation, target); err != nil {
		t.Fatal(err)
	}

	_, err := ReplaceDrifted(repo, "HEAD", "codex.global", cache, filepath.Join(t.TempDir(), "evidence"))
	if err == nil || !strings.Contains(err.Error(), "not owned by sm") {
		t.Fatalf("ReplaceDrifted error = %v, want invalid marker refusal", err)
	}
}

func TestReplaceDriftedRejectsBrokenSymlink(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "initial")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), target); err != nil {
		t.Fatal(err)
	}
	_, err := ReplaceDrifted(repo, "HEAD", "codex.global", cache, filepath.Join(t.TempDir(), "evidence"))
	if err == nil || !strings.Contains(err.Error(), "broken symlink") {
		t.Fatalf("ReplaceDrifted error = %v, want broken symlink refusal", err)
	}
}

func TestReplaceDriftedRejectsNonEmptyEvidenceDir(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	evidence := filepath.Join(t.TempDir(), "evidence")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "initial")
	applied, err := Apply(repo, "HEAD", "codex.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	driftProjectionContent(t, applied.Generation, "alpha", "drifted")
	if err := os.MkdirAll(evidence, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(evidence, "keep"), "no")
	_, err = ReplaceDrifted(repo, "HEAD", "codex.global", cache, evidence)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("ReplaceDrifted error = %v, want non-empty evidence refusal", err)
	}
}

func TestReplaceDriftedCLI(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	evidence := filepath.Join(t.TempDir(), "evidence")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "initial")
	applied, err := Apply(repo, "HEAD", "codex.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	driftProjectionContent(t, applied.Generation, "alpha", "drifted")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = RunCLI([]string{
		"replace-drifted", "--repo", repo, "--cache", cache, "--evidence-output", evidence, "codex.global",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunCLI error = %v, stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), target) || !strings.Contains(stdout.String(), "evidence\t"+evidence) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "verified codex.global") {
		t.Fatalf("stdout missing verify line: %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(evidence, replaceDriftedEvidenceName)); err != nil {
		t.Fatal(err)
	}
}

func TestApplyStillRefusesDriftedProjection(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "initial")
	applied, err := Apply(repo, "HEAD", "codex.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	driftProjectionContent(t, applied.Generation, "alpha", "drifted")
	if _, err := Apply(repo, "HEAD", "codex.global", cache); err == nil {
		t.Fatal("Apply accepted a drifted projection")
	} else if !strings.Contains(err.Error(), "drift") && !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("Apply error = %v, want drift or corrupt refusal", err)
	}
}

func driftProjectionContent(t *testing.T, generation, skillID, content string) {
	t.Helper()
	skillFile := filepath.Join(generation, skillID, "SKILL.md")
	if err := os.Chmod(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(skillFile, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, skillFile, content)
	if err := os.Chmod(skillFile, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(skillFile), 0o555); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(generation); errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
