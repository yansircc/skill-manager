package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const publicationManifestName = ".sm-publication.json"

type PublicationManifest struct {
	Schema       int      `json:"schema"`
	SourceCommit string   `json:"sourceCommit"`
	Distribution string   `json:"distribution"`
	Consumers    []string `json:"consumers"`
	Platforms    []string `json:"platforms"`
	ClosureHash  string   `json:"closureHash"`
}

// Export writes the exact union selected by a committed distribution. The
// destination is a derived tree; it is never read as source.
func Export(repo, ref, destination, distributionName string) (PublicationManifest, error) {
	root, err := repositoryRoot(repo)
	if err != nil {
		return PublicationManifest{}, err
	}
	commit, err := resolveCommit(root, ref)
	if err != nil {
		return PublicationManifest{}, err
	}
	distribution, err := loadDistribution(root, commit, distributionName)
	if err != nil {
		return PublicationManifest{}, err
	}

	consumers := distribution.Consumers
	skillSet := make(map[string]struct{})
	for _, name := range consumers {
		consumer, err := loadConsumer(root, commit, name)
		if err != nil {
			return PublicationManifest{}, err
		}
		for _, skill := range consumer.Skills {
			skillSet[skill] = struct{}{}
		}
	}
	skills := make([]string, 0, len(skillSet))
	for skill := range skillSet {
		skills = append(skills, skill)
	}
	sort.Strings(skills)

	target, err := filepath.Abs(destination)
	if err != nil {
		return PublicationManifest{}, err
	}
	targetExists, err := requireAbsentOrEmptyDirectory(target)
	if err != nil {
		return PublicationManifest{}, err
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return PublicationManifest{}, fmt.Errorf("create export parent: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".sm-export-")
	if err != nil {
		return PublicationManifest{}, fmt.Errorf("create export staging directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(staging)
		}
	}()

	if err := os.Mkdir(filepath.Join(staging, "skills"), 0o755); err != nil {
		return PublicationManifest{}, err
	}
	if err := os.Mkdir(filepath.Join(staging, "consumers"), 0o755); err != nil {
		return PublicationManifest{}, err
	}
	if err := extractSkills(root, commit, skills, filepath.Join(staging, "skills")); err != nil {
		return PublicationManifest{}, err
	}
	if err := validateDistributionExecutables(staging, skills, distribution.Platforms); err != nil {
		return PublicationManifest{}, err
	}
	for _, name := range consumers {
		contents, err := runGit(root, "show", commit+":consumers/"+name+".json")
		if err != nil {
			return PublicationManifest{}, fmt.Errorf("export consumer %q: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(staging, "consumers", name+".json"), []byte(contents), 0o644); err != nil {
			return PublicationManifest{}, err
		}
	}
	hash, err := publicationClosureHash(staging)
	if err != nil {
		return PublicationManifest{}, err
	}
	manifest := PublicationManifest{
		Schema: 2, SourceCommit: commit, Distribution: distributionName,
		Consumers: consumers, Platforms: distribution.Platforms, ClosureHash: hash,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return PublicationManifest{}, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(staging, publicationManifestName), data, 0o644); err != nil {
		return PublicationManifest{}, err
	}

	if targetExists {
		if err := os.Remove(target); err != nil {
			return PublicationManifest{}, fmt.Errorf("replace empty export destination: %w", err)
		}
	}
	if err := os.Rename(staging, target); err != nil {
		return PublicationManifest{}, fmt.Errorf("publish export: %w", err)
	}
	keep = true
	return manifest, nil
}

func validateDistributionExecutables(root string, skills, platforms []string) error {
	for _, skill := range skills {
		skillRoot := filepath.Join(root, "skills", skill)
		metadata, present, err := readOptionalSkillMetadata(skillRoot)
		if err != nil {
			return fmt.Errorf("skill %q: %w", skill, err)
		}
		if !present {
			continue
		}
		if err := validateDeclaredExecutables(skillRoot, metadata); err != nil {
			return fmt.Errorf("skill %q: %w", skill, err)
		}
		for command, declaration := range metadata.Executables {
			for _, platform := range platforms {
				parts := strings.SplitN(platform, "-", 2)
				if _, err := resolveExecutablePath(declaration, parts[0], parts[1]); err != nil {
					return fmt.Errorf("skill %q executable %q %w", skill, command, err)
				}
			}
		}
	}
	return nil
}

func requireAbsentOrEmptyDirectory(target string) (bool, error) {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("export destination exists and is not a directory: %s", target)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return false, err
	}
	if len(entries) != 0 {
		return false, fmt.Errorf("export destination is not empty: %s", target)
	}
	return true, nil
}

func publicationClosureHash(root string) (string, error) {
	hash := sha256.New()
	for _, category := range []string{"consumers", "skills"} {
		categoryRoot := filepath.Join(root, category)
		err := filepath.WalkDir(categoryRoot, func(name string, entry fs.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) && name == categoryRoot {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(root, name)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("publication contains unsupported entry: %s", relative)
			}
			executable := byte('0')
			if info.Mode()&0o111 != 0 {
				executable = '1'
			}
			fmt.Fprintf(hash, "f\x00%s\x00%c\x00", relative, executable)
			file, err := os.Open(name)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			hash.Write([]byte{0})
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
