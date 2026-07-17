package skillmanager

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type PublishReport struct {
	Producers []ProducerScan `json:"producers"`
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
	if err := os.RemoveAll(retired); err != nil {
		return PublishReport{}, fmt.Errorf("remove retired catalog: %w", err)
	}
	return PublishReport{Producers: report.Producers}, nil
}

func UpdateProducers(repo string, ids []string, stdout, stderr io.Writer) (PublishReport, error) {
	if err := Produce(repo, ids, stdout, stderr); err != nil {
		return PublishReport{}, err
	}
	return PublishProducers(repo, ids)
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
	entries, err := os.ReadDir(source)
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
	entries, err := os.ReadDir(root)
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
