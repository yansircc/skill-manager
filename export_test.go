package skillmanager

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExportCLI(t *testing.T) {
	repo := newTestRepository(t)
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{"alpha"}})
	commit := commitAll(t, repo, "catalog")
	destination := filepath.Join(t.TempDir(), "publication")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := RunCLI([]string{"export", "--repo", repo, "--ref", commit, "--consumer", "pi.global", "--output", destination}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunCLI error = %v, stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), commit) || !strings.Contains(stdout.String(), "sha256:") {
		t.Fatalf("stdout = %q, want commit and closure hash", stdout.String())
	}
	assertFileContains(t, filepath.Join(destination, "skills", "alpha", "SKILL.md"), "alpha")
}

func TestExportWritesExactCommittedAuthorizedClosure(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "active")
	writeSkill(t, repo, "alpha", "committed alpha")
	writeSkill(t, repo, "beta", "committed beta")
	writeSkill(t, repo, "private", "must not export")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "directory", Target: target, Skills: []string{"beta", "alpha"}})
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{"alpha"}})
	writeFile(t, filepath.Join(repo, "producers", "owner.json"), "{}\n")
	commit := commitAll(t, repo, "catalog")

	// Dirty working-tree data is not publication input.
	writeFile(t, filepath.Join(repo, "skills", "alpha", "SKILL.md"), "dirty alpha")
	writeSkill(t, repo, "dirty-only", "uncommitted")

	first := filepath.Join(t.TempDir(), "publication-a")
	manifest, err := Export(repo, "HEAD", first, []string{"pi.global", "codex.global"})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SourceCommit != commit {
		t.Fatalf("source commit = %q, want %q", manifest.SourceCommit, commit)
	}
	if !reflect.DeepEqual(manifest.Consumers, []string{"codex.global", "pi.global"}) {
		t.Fatalf("consumers = %#v", manifest.Consumers)
	}
	rootEntries, err := os.ReadDir(first)
	if err != nil {
		t.Fatal(err)
	}
	rootNames := make([]string, 0, len(rootEntries))
	for _, entry := range rootEntries {
		rootNames = append(rootNames, entry.Name())
	}
	if !reflect.DeepEqual(rootNames, []string{publicationManifestName, "consumers", "skills"}) {
		t.Fatalf("publication root = %#v", rootNames)
	}
	assertFileContains(t, filepath.Join(first, "skills", "alpha", "SKILL.md"), "committed alpha")
	assertFileContains(t, filepath.Join(first, "skills", "beta", "SKILL.md"), "committed beta")
	for _, excluded := range []string{"skills/private", "skills/dirty-only", "producers", executablesDir, markerName} {
		if _, err := os.Lstat(filepath.Join(first, excluded)); !os.IsNotExist(err) {
			t.Fatalf("excluded path %q exists or stat failed: %v", excluded, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(first, publicationManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var recorded PublicationManifest
	if err := json.Unmarshal(data, &recorded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorded, manifest) || !strings.HasPrefix(recorded.ClosureHash, "sha256:") {
		t.Fatalf("recorded manifest = %#v, returned = %#v", recorded, manifest)
	}

	second := filepath.Join(t.TempDir(), "publication-b")
	secondManifest, err := Export(repo, commit, second, []string{"codex.global", "pi.global"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(secondManifest, manifest) {
		t.Fatalf("second manifest = %#v, want %#v", secondManifest, manifest)
	}

	// The exported tree is the existing Build input, not a second package format.
	if _, err := runGit(first, "init"); err != nil {
		t.Fatal(err)
	}
	publicationCommit := commitAll(t, first, "publication")
	roundTripHash, err := publicationClosureHash(first)
	if err != nil {
		t.Fatal(err)
	}
	if roundTripHash != manifest.ClosureHash {
		t.Fatalf("Git worktree closure hash = %q, want %q", roundTripHash, manifest.ClosureHash)
	}
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	result, err := Build(first, publicationCommit, "codex.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(result.Generation, "alpha", "SKILL.md"), "committed alpha")
}

func TestExportPreservesExecutableMode(t *testing.T) {
	repo := newTestRepository(t)
	writeExecutableSkill(t, repo, "alpha", "alpha", "ok")
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{"alpha"}})
	commitAll(t, repo, "catalog")

	destination := filepath.Join(t.TempDir(), "publication")
	if _, err := Export(repo, "HEAD", destination, []string{"pi.global"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(destination, "skills", "alpha", "bin", "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("exported executable lost executable mode")
	}
}

func TestExportFailureDoesNotPublishPartialTree(t *testing.T) {
	repo := newTestRepository(t)
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{"missing"}})
	commitAll(t, repo, "catalog")
	destination := filepath.Join(t.TempDir(), "publication")

	if _, err := Export(repo, "HEAD", destination, []string{"pi.global"}); err == nil {
		t.Fatal("Export succeeded with missing skill")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("partial destination exists or stat failed: %v", err)
	}
}

func TestExportRefusesNonEmptyDestination(t *testing.T) {
	repo := newTestRepository(t)
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{"alpha"}})
	commitAll(t, repo, "catalog")
	destination := filepath.Join(t.TempDir(), "publication")
	writeFile(t, filepath.Join(destination, "owned"), "keep")

	if _, err := Export(repo, "HEAD", destination, []string{"pi.global"}); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("Export error = %v, want non-empty refusal", err)
	}
	assertFileContains(t, filepath.Join(destination, "owned"), "keep")
}

func TestExportAcceptsEmptyDestination(t *testing.T) {
	repo := newTestRepository(t)
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{"alpha"}})
	commitAll(t, repo, "catalog")
	destination := filepath.Join(t.TempDir(), "publication")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Export(repo, "HEAD", destination, []string{"pi.global"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(destination, "skills", "alpha", "SKILL.md"), "alpha")
}
