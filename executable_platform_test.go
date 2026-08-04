package skillmanager

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveExecutablePathPrefersExactPlatform(t *testing.T) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	declaration := ExecutableDeclaration{
		"any":    "bin/portable",
		platform: "bin/native",
	}
	got, err := resolveExecutablePath(declaration, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if got != "bin/native" {
		t.Fatalf("resolved path = %q, want exact platform artifact", got)
	}
}

func TestBuildFailsWithoutCurrentExecutablePlatform(t *testing.T) {
	repo := newTestRepository(t)
	otherPlatform := "linux-amd64"
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		otherPlatform = "darwin-arm64"
	}
	root := filepath.Join(repo, "skills", "alpha")
	writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: alpha\ndescription: native test\nexecutables:\n  alpha:\n    "+otherPlatform+": bin/alpha\n---\n")
	path := filepath.Join(root, "bin", "alpha")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("native"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{"alpha"}})
	commitAll(t, repo, "catalog")

	_, err := Build(repo, "HEAD", "pi.global", filepath.Join(t.TempDir(), "cache"))
	if err == nil || !strings.Contains(err.Error(), "has no artifact for platform "+runtime.GOOS+"-"+runtime.GOARCH) {
		t.Fatalf("Build error = %v, want missing current platform failure", err)
	}
}

func TestExecutableDeclarationRejectsScalarPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: alpha\ndescription: legacy declaration\nexecutables:\n  alpha: bin/alpha\n---\n")
	_, err := readSkillMetadata(root)
	if err == nil || !strings.Contains(err.Error(), "must map platforms to paths") {
		t.Fatalf("readSkillMetadata error = %v, want platform map requirement", err)
	}
}
