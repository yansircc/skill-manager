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
)

const publicationManifestName = ".sm-publication.json"

type PublicationManifest struct {
	Schema       int      `json:"schema"`
	SourceCommit string   `json:"sourceCommit"`
	Consumers    []string `json:"consumers"`
	ClosureHash  string   `json:"closureHash"`
}

// Export writes the exact union of the selected consumers' committed skill
// allowlists. The destination is a derived tree; it is never read as source.
func Export(repo, ref, destination string, consumerNames []string) (PublicationManifest, error) {
	if len(consumerNames) == 0 {
		return PublicationManifest{}, fmt.Errorf("at least one consumer is required")
	}
	root, err := repositoryRoot(repo)
	if err != nil {
		return PublicationManifest{}, err
	}
	commit, err := resolveCommit(root, ref)
	if err != nil {
		return PublicationManifest{}, err
	}

	consumers := append([]string(nil), consumerNames...)
	sort.Strings(consumers)
	skillSet := make(map[string]struct{})
	for index, name := range consumers {
		if index > 0 && name == consumers[index-1] {
			return PublicationManifest{}, fmt.Errorf("duplicate consumer %q", name)
		}
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
	manifest := PublicationManifest{Schema: 1, SourceCommit: commit, Consumers: consumers, ClosureHash: hash}
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
