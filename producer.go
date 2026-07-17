package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type ProducerBuild struct {
	Argv []string `json:"argv"`
}

type ProducerOutput struct {
	Path string `json:"path"`
}

type Producer struct {
	ID      string           `json:"id"`
	Root    string           `json:"root"`
	Build   ProducerBuild    `json:"build"`
	Outputs []ProducerOutput `json:"outputs"`
	Skills  []string         `json:"skills"`
}

type ArtifactState string

const (
	ArtifactNew       ArtifactState = "new"
	ArtifactUnchanged ArtifactState = "unchanged"
	ArtifactUpdated   ArtifactState = "updated"
	ArtifactConflict  ArtifactState = "conflict"
	ArtifactInvalid   ArtifactState = "invalid"
)

type Artifact struct {
	ProducerID string        `json:"producerId"`
	SkillID    string        `json:"skillId"`
	Path       string        `json:"path"`
	TreeHash   string        `json:"treeHash,omitempty"`
	State      ArtifactState `json:"state"`
	Error      string        `json:"error,omitempty"`
}

type ProducerScan struct {
	Producer  Producer   `json:"producer"`
	Artifacts []Artifact `json:"artifacts"`
	Error     string     `json:"error,omitempty"`
}

type ScanReport struct {
	Producers []ProducerScan `json:"producers"`
}

func loadProducers(repo string) ([]Producer, error) {
	root, err := repositoryRoot(repo)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(root, "producers")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []Producer{}, nil
	}
	if err != nil {
		return nil, err
	}
	var producers []Producer
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		var producer Producer
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&producer); err != nil {
			return nil, fmt.Errorf("parse producer %s: %w", id, err)
		}
		producer.ID = id
		if err := validateProducer(producer); err != nil {
			return nil, fmt.Errorf("producer %s: %w", id, err)
		}
		producers = append(producers, producer)
	}
	sort.Slice(producers, func(i, j int) bool { return producers[i].ID < producers[j].ID })
	return producers, nil
}

func validateProducer(producer Producer) error {
	if err := validateID(producer.ID); err != nil {
		return err
	}
	if !filepath.IsAbs(producer.Root) {
		return fmt.Errorf("root must be absolute")
	}
	if len(producer.Build.Argv) == 0 || producer.Build.Argv[0] == "" {
		return fmt.Errorf("build.argv must not be empty")
	}
	if len(producer.Outputs) == 0 {
		return fmt.Errorf("outputs must not be empty")
	}
	for _, output := range producer.Outputs {
		if output.Path == "" {
			return fmt.Errorf("output path must not be empty")
		}
	}
	if len(producer.Skills) == 0 {
		return fmt.Errorf("skills must not be empty; ownership must be explicit")
	}
	seen := map[string]bool{}
	for _, skill := range producer.Skills {
		if err := validateID(skill); err != nil {
			return fmt.Errorf("skill %q: %w", skill, err)
		}
		if seen[skill] {
			return fmt.Errorf("duplicate owned skill %q", skill)
		}
		seen[skill] = true
	}
	return nil
}

func selectProducers(all []Producer, ids []string) ([]Producer, error) {
	if len(ids) == 0 {
		return all, nil
	}
	byID := make(map[string]Producer, len(all))
	for _, producer := range all {
		byID[producer.ID] = producer
	}
	selected := make([]Producer, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		producer, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("unknown producer %q", id)
		}
		if !seen[id] {
			selected = append(selected, producer)
			seen[id] = true
		}
	}
	return selected, nil
}

func ScanProducers(repo string, ids []string) (ScanReport, error) {
	all, err := loadProducers(repo)
	if err != nil {
		return ScanReport{}, err
	}
	selected, err := selectProducers(all, ids)
	if err != nil {
		return ScanReport{}, err
	}
	repository, err := repositoryRoot(repo)
	if err != nil {
		return ScanReport{}, err
	}
	report := ScanReport{}
	configuredOwners := map[string]string{}
	for _, producer := range all {
		for _, skill := range producer.Skills {
			if owner, exists := configuredOwners[skill]; exists {
				return ScanReport{}, fmt.Errorf("skill %q is owned by both %s and %s", skill, owner, producer.ID)
			}
			configuredOwners[skill] = producer.ID
		}
	}
	owners := map[string][]*Artifact{}
	for _, producer := range selected {
		artifacts, discoverErr := discoverArtifacts(repository, producer)
		scan := ProducerScan{Producer: producer, Artifacts: artifacts}
		if discoverErr != nil {
			scan.Error = discoverErr.Error()
		}
		report.Producers = append(report.Producers, scan)
		for index := range report.Producers[len(report.Producers)-1].Artifacts {
			artifact := &report.Producers[len(report.Producers)-1].Artifacts[index]
			owners[artifact.SkillID] = append(owners[artifact.SkillID], artifact)
		}
	}
	for _, artifacts := range owners {
		if len(artifacts) > 1 {
			for _, artifact := range artifacts {
				artifact.State = ArtifactConflict
				artifact.Error = "skill is emitted by multiple producers"
			}
		}
	}
	return report, nil
}

func discoverArtifacts(repository string, producer Producer) ([]Artifact, error) {
	var roots []string
	for _, output := range producer.Outputs {
		root := output.Path
		if !filepath.IsAbs(root) {
			root = filepath.Join(producer.Root, root)
		}
		root, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, fmt.Errorf("resolve output %s: %w", output.Path, err)
		}
		if within(filepath.Join(repository, "skills"), root) {
			return nil, fmt.Errorf("output is inside the canonical catalog: %s", root)
		}
		roots = append(roots, root)
	}
	paths := map[string]struct{}{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && entry.Name() == ".git" {
				return filepath.SkipDir
			}
			if !entry.IsDir() && entry.Name() == "SKILL.md" {
				paths[filepath.Dir(name)] = struct{}{}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	artifacts := make([]Artifact, 0, len(ordered))
	seen := map[string]string{}
	for _, path := range ordered {
		metadata, err := readSkillMetadata(path)
		artifact := Artifact{ProducerID: producer.ID, Path: path}
		if err != nil {
			artifact.State = ArtifactInvalid
			artifact.Error = err.Error()
			artifacts = append(artifacts, artifact)
			continue
		}
		artifact.SkillID = metadata.Name
		owned := false
		for _, id := range producer.Skills {
			if id == artifact.SkillID {
				owned = true
				break
			}
		}
		if !owned {
			artifact.State = ArtifactConflict
			artifact.Error = "skill is not declared in producer ownership"
			artifacts = append(artifacts, artifact)
			continue
		}
		if first, duplicate := seen[artifact.SkillID]; duplicate {
			artifact.State = ArtifactConflict
			artifact.Error = "duplicate with " + first
			artifacts = append(artifacts, artifact)
			continue
		}
		seen[artifact.SkillID] = path
		if err := validateCanonicalSkill(path); err != nil {
			artifact.State = ArtifactInvalid
			artifact.Error = err.Error()
			artifacts = append(artifacts, artifact)
			continue
		}
		artifact.TreeHash, err = hashTree(path)
		if err != nil {
			artifact.State = ArtifactInvalid
			artifact.Error = err.Error()
			artifacts = append(artifacts, artifact)
			continue
		}
		catalogPath := filepath.Join(repository, "skills", artifact.SkillID)
		catalogHash, err := hashTree(catalogPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
			artifact.State = ArtifactNew
		case err != nil:
			artifact.State = ArtifactInvalid
			artifact.Error = err.Error()
		case catalogHash == artifact.TreeHash:
			artifact.State = ArtifactUnchanged
		default:
			artifact.State = ArtifactUpdated
		}
		artifacts = append(artifacts, artifact)
	}
	for _, id := range producer.Skills {
		if _, ok := seen[id]; !ok {
			artifacts = append(artifacts, Artifact{ProducerID: producer.ID, SkillID: id, State: ArtifactInvalid, Error: "declared skill was not found in outputs"})
		}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].SkillID < artifacts[j].SkillID })
	return artifacts, nil
}

func Produce(repo string, ids []string, stdout, stderr io.Writer) error {
	all, err := loadProducers(repo)
	if err != nil {
		return err
	}
	selected, err := selectProducers(all, ids)
	if err != nil {
		return err
	}
	for _, producer := range selected {
		command := exec.Command(producer.Build.Argv[0], producer.Build.Argv[1:]...)
		command.Dir = producer.Root
		command.Stdin = nil
		command.Stdout = stdout
		command.Stderr = stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("produce %s: %w", producer.ID, err)
		}
	}
	return nil
}
