package skillmanager

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProducerRelocateChangesOnlyTheLocator(t *testing.T) {
	repo := newTestRepository(t)
	configureTestGitIdentity(t, repo)
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "catalog alpha")
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	writeNamedSkill(t, filepath.Join(newRoot, "dist", "alpha"), "alpha", "new alpha")
	producer := Producer{
		ID: "example", Root: oldRoot, Build: ProducerBuild{Argv: []string{"true"}},
		Outputs: []ProducerOutput{{Path: "dist"}}, Skills: []string{"alpha"},
	}
	if err := writeProducer(repo, producer); err != nil {
		t.Fatal(err)
	}
	before := commitAll(t, repo, "initial")

	var stdout, stderr bytes.Buffer
	if err := RunCLI([]string{"producer", "relocate", "--repo", repo, "example", newRoot}, &stdout, &stderr); err != nil {
		t.Fatalf("relocate: %v; stderr=%s", err, stderr.String())
	}
	producers, err := loadProducers(repo)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(producers) != 1 || producers[0].Root != resolvedRoot {
		t.Fatalf("producers = %#v", producers)
	}
	metadata, err := readSkillMetadata(filepath.Join(repo, "skills", "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Description != "catalog alpha" {
		t.Fatalf("relocation changed catalog: %q", metadata.Description)
	}
	after, err := resolveCommit(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatal("relocation was not committed")
	}
	assertCleanRepository(t, repo)
}

func TestProducerRelocateRejectsIncompleteOwnershipWithoutMutation(t *testing.T) {
	repo := newTestRepository(t)
	configureTestGitIdentity(t, repo)
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	writeNamedSkill(t, filepath.Join(newRoot, "dist", "beta"), "beta", "wrong skill")
	producer := Producer{
		ID: "example", Root: oldRoot, Build: ProducerBuild{Argv: []string{"true"}},
		Outputs: []ProducerOutput{{Path: "dist"}}, Skills: []string{"alpha"},
	}
	if err := writeProducer(repo, producer); err != nil {
		t.Fatal(err)
	}
	before := commitAll(t, repo, "initial")

	err := RelocateProducer(repo, "example", newRoot, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "validate relocated producer") {
		t.Fatalf("relocate error = %v", err)
	}
	producers, loadErr := loadProducers(repo)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if producers[0].Root != oldRoot {
		t.Fatalf("failed relocation changed root to %q", producers[0].Root)
	}
	after, resolveErr := resolveCommit(repo, "HEAD")
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if after != before {
		t.Fatal("failed relocation changed HEAD")
	}
	assertCleanRepository(t, repo)
}

func TestProducerRelocateRequiresCleanSSOT(t *testing.T) {
	repo := newTestRepository(t)
	configureTestGitIdentity(t, repo)
	root := t.TempDir()
	writeNamedSkill(t, filepath.Join(root, "dist", "alpha"), "alpha", "alpha")
	producer := Producer{
		ID: "example", Root: t.TempDir(), Build: ProducerBuild{Argv: []string{"true"}},
		Outputs: []ProducerOutput{{Path: "dist"}}, Skills: []string{"alpha"},
	}
	if err := writeProducer(repo, producer); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "initial")
	writeFile(t, filepath.Join(repo, "unrelated.txt"), "dirty")

	err := RelocateProducer(repo, "example", root, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "must be clean") {
		t.Fatalf("relocate error = %v", err)
	}
}

func TestProducerRelocateRestoresLocatorWhenCommitFails(t *testing.T) {
	repo := newTestRepository(t)
	configureTestGitIdentity(t, repo)
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	writeNamedSkill(t, filepath.Join(newRoot, "dist", "alpha"), "alpha", "alpha")
	producer := Producer{
		ID: "example", Root: oldRoot, Build: ProducerBuild{Argv: []string{"true"}},
		Outputs: []ProducerOutput{{Path: "dist"}}, Skills: []string{"alpha"},
	}
	if err := writeProducer(repo, producer); err != nil {
		t.Fatal(err)
	}
	before := commitAll(t, repo, "initial")
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := RelocateProducer(repo, "example", newRoot, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "commit relocation") {
		t.Fatalf("relocate error = %v", err)
	}
	producers, loadErr := loadProducers(repo)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if producers[0].Root != oldRoot {
		t.Fatalf("failed commit changed root to %q", producers[0].Root)
	}
	after, resolveErr := resolveCommit(repo, "HEAD")
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if after != before {
		t.Fatal("failed commit changed HEAD")
	}
	assertCleanRepository(t, repo)
}

func configureTestGitIdentity(t *testing.T, repo string) {
	t.Helper()
	if _, err := runGit(repo, "config", "user.name", "sm-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repo, "config", "user.email", "sm-test@example.com"); err != nil {
		t.Fatal(err)
	}
}

func assertCleanRepository(t *testing.T, repo string) {
	t.Helper()
	status, err := runGit(repo, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		t.Fatalf("repository is dirty: %s", status)
	}
}
