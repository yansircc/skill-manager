package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type Candidate struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

func Scan(repo string, roots []string) ([]Candidate, error) {
	repository, err := repositoryRoot(repo)
	if err != nil {
		return nil, err
	}
	catalog := filepath.Join(repository, "skills")
	canonicalCatalog, err := canonicalPath(catalog)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]struct{})
	for _, root := range roots {
		root, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return nil, fmt.Errorf("resolve discovery root %s: %w", root, err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("discovery root is not a directory: %s", root)
		}
		err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && entry.Name() == ".git" {
				return filepath.SkipDir
			}
			if entry.IsDir() || entry.Name() != "SKILL.md" {
				return nil
			}
			candidate, err := filepath.EvalSymlinks(filepath.Dir(name))
			if err != nil {
				return err
			}
			if within(canonicalCatalog, candidate) {
				return nil
			}
			paths[candidate] = struct{}{}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", root, err)
		}
	}

	orderedPaths := make([]string, 0, len(paths))
	for candidate := range paths {
		orderedPaths = append(orderedPaths, candidate)
	}
	sort.Strings(orderedPaths)
	candidates := make([]Candidate, 0, len(orderedPaths))
	identities := make(map[string]string, len(orderedPaths))
	for _, candidatePath := range orderedPaths {
		if err := validateCanonicalSkill(candidatePath); err != nil {
			return nil, fmt.Errorf("candidate %s: %w", candidatePath, err)
		}
		id := filepath.Base(candidatePath)
		if err := validateID(id); err != nil {
			return nil, fmt.Errorf("candidate %s: %w", candidatePath, err)
		}
		if existing, duplicate := identities[id]; duplicate {
			return nil, fmt.Errorf("duplicate candidate id %q: %s and %s", id, existing, candidatePath)
		}
		if _, err := os.Lstat(filepath.Join(catalog, id)); err == nil {
			return nil, fmt.Errorf("candidate id %q already exists in the SSOT catalog", id)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		identities[id] = candidatePath
		candidates = append(candidates, Candidate{ID: id, Path: candidatePath})
	}
	return candidates, nil
}

func canonicalPath(name string) (string, error) {
	if canonical, err := filepath.EvalSymlinks(name); err == nil {
		return canonical, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(name))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(name)), nil
}
