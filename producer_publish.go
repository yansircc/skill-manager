package skillmanager

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type PublishReport struct {
	Producers []ProducerScan   `json:"producers"`
	Sources   []ProducerSource `json:"sources,omitempty"`
	Handoff   CatalogHandoff   `json:"handoff"`
}

type ProducerSource struct {
	ProducerID   string `json:"producerId"`
	ObservedHead string `json:"observedHead,omitempty"`
	Dirty        bool   `json:"dirty"`
	Git          bool   `json:"git"`
}

type CatalogHandoff struct {
	Head                           string   `json:"head"`
	ChangedFiles                   []string `json:"changedFiles"`
	PendingCommit                  bool     `json:"pendingCommit"`
	BuildReadsCommittedCatalogOnly bool     `json:"buildReadsCommittedCatalogOnly"`
	NextCommand                    []string `json:"nextCommand,omitempty"`
}

func PublishProducers(repo string, ids []string) (PublishReport, error) {
	root, err := repositoryRoot(repo)
	if err != nil {
		return PublishReport{}, err
	}
	lock, err := lockRepository(root)
	if err != nil {
		return PublishReport{}, err
	}
	defer unlockRepository(lock)
	report, err := ScanProducers(root, ids)
	if err != nil {
		return PublishReport{}, err
	}
	all, err := loadProducers(root)
	if err != nil {
		return PublishReport{}, err
	}
	selected, err := selectProducers(all, ids)
	if err != nil {
		return PublishReport{}, err
	}
	artifacts := map[string]Artifact{}
	for _, scan := range report.Producers {
		if scan.Error != "" {
			return PublishReport{}, fmt.Errorf("producer %s: %s", scan.Producer.ID, scan.Error)
		}
		for _, artifact := range scan.Artifacts {
			if artifact.State == ArtifactInvalid || artifact.State == ArtifactConflict {
				return PublishReport{}, fmt.Errorf("producer %s skill %s: %s", artifact.ProducerID, artifact.SkillID, artifact.Error)
			}
			artifacts[artifact.SkillID] = artifact
		}
	}
	catalog := filepath.Join(root, "skills")
	stage, err := os.MkdirTemp(root, ".sm-catalog-stage-")
	if err != nil {
		return PublishReport{}, err
	}
	defer os.RemoveAll(stage)
	if err := copyCatalog(catalog, stage); err != nil {
		return PublishReport{}, fmt.Errorf("stage catalog: %w", err)
	}
	for _, producer := range selected {
		for _, skill := range producer.Skills {
			if err := os.RemoveAll(filepath.Join(stage, skill)); err != nil {
				return PublishReport{}, err
			}
		}
	}
	for skill, artifact := range artifacts {
		destination := filepath.Join(stage, skill)
		if err := os.Mkdir(destination, 0o755); err != nil {
			return PublishReport{}, err
		}
		if err := copyCanonicalSkill(artifact.Path, destination); err != nil {
			return PublishReport{}, fmt.Errorf("stage %s: %w", skill, err)
		}
	}
	if err := validateCatalog(stage); err != nil {
		return PublishReport{}, err
	}
	retired := filepath.Join(root, ".sm-catalog-retired")
	if _, err := os.Lstat(retired); !errors.Is(err, os.ErrNotExist) {
		return PublishReport{}, fmt.Errorf("catalog recovery required: %s exists", retired)
	}
	if err := os.Rename(catalog, retired); err != nil {
		return PublishReport{}, fmt.Errorf("retire catalog: %w", err)
	}
	if err := os.Rename(stage, catalog); err != nil {
		_ = os.Rename(retired, catalog)
		return PublishReport{}, fmt.Errorf("promote catalog: %w", err)
	}
	handoff, err := catalogHandoff(root)
	if err != nil {
		_ = os.RemoveAll(catalog)
		_ = os.Rename(retired, catalog)
		return PublishReport{}, err
	}
	if err := os.RemoveAll(retired); err != nil {
		return PublishReport{}, fmt.Errorf("remove retired catalog: %w", err)
	}
	return PublishReport{Producers: report.Producers, Handoff: handoff}, nil
}

func UpdateProducers(repo string, ids []string, stdout, stderr io.Writer) (PublishReport, error) {
	if err := Produce(repo, ids, stdout, stderr); err != nil {
		return PublishReport{}, err
	}
	report, err := PublishProducers(repo, ids)
	if err != nil {
		return PublishReport{}, err
	}
	for _, producer := range report.Producers {
		report.Sources = append(report.Sources, observeProducerSource(producer.Producer))
	}
	return report, nil
}

func catalogHandoff(root string) (CatalogHandoff, error) {
	changedSet := make(map[string]struct{})
	headOutput, headErr := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD^{commit}").Output()
	head := strings.TrimSpace(string(headOutput))
	if headErr == nil {
		output, err := runGit(root, "diff", "--name-only", "HEAD", "--", "skills", "producers", "consumers", ".gitignore")
		if err != nil {
			return CatalogHandoff{}, err
		}
		for _, name := range strings.Split(strings.TrimSpace(output), "\n") {
			if name != "" {
				changedSet[name] = struct{}{}
			}
		}
	} else {
		tracked, err := runGit(root, "ls-files", "--", "skills", "producers", "consumers", ".gitignore")
		if err != nil {
			return CatalogHandoff{}, err
		}
		for _, name := range strings.Split(strings.TrimSpace(tracked), "\n") {
			if name != "" {
				changedSet[name] = struct{}{}
			}
		}
	}
	untracked, err := runGit(root, "ls-files", "--others", "--exclude-standard", "--", "skills", "producers", "consumers", ".gitignore")
	if err != nil {
		return CatalogHandoff{}, err
	}
	for _, name := range strings.Split(strings.TrimSpace(untracked), "\n") {
		if name != "" {
			changedSet[name] = struct{}{}
		}
	}
	changed := make([]string, 0, len(changedSet))
	for name := range changedSet {
		changed = append(changed, name)
	}
	sort.Strings(changed)
	handoff := CatalogHandoff{
		Head:                           head,
		ChangedFiles:                   changed,
		PendingCommit:                  len(changed) != 0,
		BuildReadsCommittedCatalogOnly: true,
	}
	if handoff.PendingCommit {
		handoff.NextCommand = []string{"git", "-C", root, "status", "--short", "--", "skills", "producers", "consumers", ".gitignore"}
	}
	return handoff, nil
}

func observeProducerSource(producer Producer) ProducerSource {
	source := ProducerSource{ProducerID: producer.ID}
	command := exec.Command("git", "-C", producer.Root, "rev-parse", "--show-toplevel")
	if err := command.Run(); err != nil {
		return source
	}
	source.Git = true
	if output, err := exec.Command("git", "-C", producer.Root, "rev-parse", "HEAD").Output(); err == nil {
		source.ObservedHead = strings.TrimSpace(string(output))
	}
	if output, err := exec.Command("git", "-C", producer.Root, "status", "--porcelain", "--untracked-files=normal").Output(); err == nil {
		source.Dirty = strings.TrimSpace(string(output)) != ""
	} else {
		source.Dirty = true
	}
	return source
}

func lockRepository(root string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(root, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func unlockRepository(file *os.File) {
	_ = unlockFile(file)
	_ = file.Close()
}

func copyCatalog(source, destination string) error {
	entries, err := catalogEntries(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		target := filepath.Join(destination, entry.Name())
		if err := os.Mkdir(target, 0o755); err != nil {
			return err
		}
		if err := copyCanonicalSkill(filepath.Join(source, entry.Name()), target); err != nil {
			return err
		}
	}
	return nil
}

func validateCatalog(root string) error {
	entries, err := catalogEntries(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return fmt.Errorf("catalog contains non-skill entry %s", entry.Name())
		}
		path := filepath.Join(root, entry.Name())
		if err := validateCanonicalSkill(path); err != nil {
			return fmt.Errorf("skill %s: %w", entry.Name(), err)
		}
		metadata, err := readSkillMetadata(path)
		if err != nil {
			return fmt.Errorf("skill %s: %w", entry.Name(), err)
		}
		if metadata.Name != entry.Name() {
			return fmt.Errorf("skill directory %q does not match frontmatter name %q", entry.Name(), metadata.Name)
		}
	}
	return nil
}

// catalogEntries returns every filesystem-backed catalog fact. Git cannot
// represent empty directories, so recursively empty directory trees are not
// skills and must not affect catalog behavior after tracked files are removed.
func catalogEntries(root string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	filtered := entries[:0]
	for _, entry := range entries {
		if entry.IsDir() {
			hasFiles, err := directoryHasFiles(filepath.Join(root, entry.Name()))
			if err != nil {
				return nil, err
			}
			if !hasFiles {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	return filtered, nil
}

func directoryHasFiles(root string) (bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return true, nil
		}
		hasFiles, err := directoryHasFiles(filepath.Join(root, entry.Name()))
		if err != nil {
			return false, err
		}
		if hasFiles {
			return true, nil
		}
	}
	return false, nil
}
