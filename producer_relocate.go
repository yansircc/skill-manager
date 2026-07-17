package skillmanager

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func RelocateProducer(repo, id, newRoot string, stdout, stderr io.Writer) error {
	repository, err := repositoryRoot(repo)
	if err != nil {
		return err
	}
	status, err := runGit(repository, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("SSOT must be clean before relocating a producer")
	}
	producers, err := loadProducers(repository)
	if err != nil {
		return err
	}
	selected, err := selectProducers(producers, []string{id})
	if err != nil {
		return err
	}
	root, err := expandHome(newRoot)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve new root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("inspect new root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("new root is not a directory: %s", root)
	}

	original := selected[0]
	candidate := original
	candidate.Root = root
	if err := runProducerBuild(candidate, stdout, stderr); err != nil {
		return err
	}
	artifacts, err := discoverArtifacts(repository, candidate)
	if err != nil {
		return fmt.Errorf("validate relocated producer: %w", err)
	}
	for _, artifact := range artifacts {
		if artifact.State == ArtifactInvalid || artifact.State == ArtifactConflict {
			return fmt.Errorf("validate relocated producer skill %s: %s", artifact.SkillID, artifact.Error)
		}
	}
	if candidate.Root == original.Root {
		return nil
	}

	path := filepath.Join(repository, "producers", id+".json")
	previous, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := writeProducer(repository, candidate); err != nil {
		return err
	}
	if err := commitSSOT(repository, "Relocate producer "+id); err != nil {
		restoreErr := writeFileAtomic(path, previous, 0o644)
		if restoreErr == nil {
			_, restoreErr = runGit(repository, "add", filepath.Join("producers", id+".json"))
		}
		if restoreErr != nil {
			return fmt.Errorf("commit relocation: %v; restore producer: %w", err, restoreErr)
		}
		return fmt.Errorf("commit relocation: %w", err)
	}
	return nil
}
