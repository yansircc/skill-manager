package skillmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardSkillFilesAreConfinedAndPreviewed(t *testing.T) {
	repo := newTestRepository(t)
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "Alpha skill")
	writeFile(t, filepath.Join(repo, "skills", "alpha", "scripts", "verify.sh"), "#!/bin/sh\necho ok\n")
	writeFile(t, filepath.Join(repo, "outside.txt"), "outside")

	files, err := listDashboardSkillFiles(repo, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Files) != 3 || files.Files[0].Path != "SKILL.md" || files.Files[1].Path != "scripts" || !files.Files[1].Directory || files.Files[2].Path != "scripts/verify.sh" {
		t.Fatalf("files = %#v", files.Files)
	}
	content, err := readDashboardSkillFile(repo, "alpha", "scripts/verify.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !content.Preview || content.Language != "bash" || content.Content != "#!/bin/sh\necho ok\n" {
		t.Fatalf("content = %#v", content)
	}
	if _, err := readDashboardSkillFile(repo, "alpha", "../outside.txt"); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("traversal error = %v", err)
	}
}

func TestDashboardSkillFileRejectsSymlinkEscape(t *testing.T) {
	repo := newTestRepository(t)
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "Alpha skill")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(repo, "skills", "alpha", "outside.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := readDashboardSkillFile(repo, "alpha", "outside.txt"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestDashboardSkillFileMarksBinaryAndLargeFilesUnavailable(t *testing.T) {
	repo := newTestRepository(t)
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "Alpha skill")
	if err := os.WriteFile(filepath.Join(repo, "skills", "alpha", "binary.bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "alpha", "large.txt"), make([]byte, dashboardFilePreviewLimit+1), 0o644); err != nil {
		t.Fatal(err)
	}
	binary, err := readDashboardSkillFile(repo, "alpha", "binary.bin")
	if err != nil {
		t.Fatal(err)
	}
	if binary.Preview || !strings.Contains(binary.Unavailable, "二进制") {
		t.Fatalf("binary = %#v", binary)
	}
	large, err := readDashboardSkillFile(repo, "alpha", "large.txt")
	if err != nil {
		t.Fatal(err)
	}
	if large.Preview || !strings.Contains(large.Unavailable, "512 KB") {
		t.Fatalf("large = %#v", large)
	}
}
