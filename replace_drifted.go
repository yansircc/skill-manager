package skillmanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const replaceDriftedEvidenceName = "replace-drifted.json"

// ReplaceDriftedResult is the activation outcome plus durable evidence of the
// drifted generation that was preserved and replaced.
type ReplaceDriftedResult struct {
	Result
	EvidenceDir  string
	Evidence     ReplaceDriftedEvidence
	Verification Result
}

// ReplaceDriftedEvidence records enough metadata to audit a drifted-projection
// replacement without copying skill file contents.
type ReplaceDriftedEvidence struct {
	Schema              int          `json:"schema"`
	Timestamp           string       `json:"timestamp"`
	RepoCommit          string       `json:"repoCommit"`
	Consumer            string       `json:"consumer"`
	OldTarget           string       `json:"oldTarget"`
	OldGeneration       string       `json:"oldGeneration"`
	PreservedGeneration string       `json:"preservedGeneration"`
	OldMarker           Marker       `json:"oldMarker"`
	OldExpectedTreeHash string       `json:"oldExpectedTreeHash"`
	OldActualTreeHash   string       `json:"oldActualTreeHash"`
	Changes             []TreeChange `json:"changes,omitempty"`
	NewGeneration       string       `json:"newGeneration"`
	NewMarker           Marker       `json:"newMarker"`
}

// TreeChange names a path whose entry type or content differs between the
// marker-expected projection and the drifted active generation.
type TreeChange struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

// ReplaceDrifted builds the desired generation, replaces an sm-owned active
// symlink whose projection may have content-hash drift, preserves the old
// generation, writes durable evidence, then verifies the new activation.
func ReplaceDrifted(repo, ref, consumerName, cache, evidenceOutput string) (ReplaceDriftedResult, error) {
	root, err := repositoryRoot(repo)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}
	commit, err := resolveCommit(root, ref)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}
	consumer, err := loadConsumer(root, commit, consumerName)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}
	adapter, err := lookupAgentAdapter(consumer.Adapter)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}
	if !adapter.Persistent() {
		return ReplaceDriftedResult{}, fmt.Errorf("consumer %q uses %s activation; use sm exec", consumerName, consumer.Adapter)
	}
	cache, err = cacheRoot(cache)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}
	compiler, err := compilerIdentity()
	if err != nil {
		return ReplaceDriftedResult{}, err
	}
	evidencePath, err := filepath.Abs(evidenceOutput)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}
	evidenceExists, err := requireAbsentOrEmptyDirectory(evidencePath)
	if err != nil {
		return ReplaceDriftedResult{}, fmt.Errorf("evidence output: %w", err)
	}

	target, err := expandHome(consumer.Target)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}
	oldGeneration, oldMarker, oldActualHash, err := inspectDriftedActiveTarget(target, consumerName)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}

	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	changes, err := driftedProjectionChanges(root, oldGeneration, oldMarker)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}

	desiredGeneration := filepath.Join(cache, "generations", generationKey(commit, consumerName, compiler))
	preservedGeneration := oldGeneration
	if samePath(oldGeneration, desiredGeneration) {
		preservedGeneration, err = preserveGeneration(oldGeneration, timestamp)
		if err != nil {
			return ReplaceDriftedResult{}, err
		}
	}

	built, err := Build(repo, ref, consumerName, cache)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}
	newMarker, err := readGenerationMarker(built.Generation)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}

	evidence := ReplaceDriftedEvidence{
		Schema:              1,
		Timestamp:           timestamp,
		RepoCommit:          commit,
		Consumer:            consumerName,
		OldTarget:           target,
		OldGeneration:       oldGeneration,
		PreservedGeneration: preservedGeneration,
		OldMarker:           oldMarker,
		OldExpectedTreeHash: oldMarker.TreeHash,
		OldActualTreeHash:   oldActualHash,
		Changes:             changes,
		NewGeneration:       built.Generation,
		NewMarker:           newMarker,
	}
	if err := writeReplaceDriftedEvidence(evidencePath, evidenceExists, evidence); err != nil {
		return ReplaceDriftedResult{}, err
	}

	previousExecutablesTarget := oldMarker.ExecutablesTarget
	if previousExecutablesTarget == "" {
		// Prefer the live target when the marker predates executablesTarget.
		if live, liveErr := activeExecutablesTargetAllowingDrift(target, consumerName); liveErr == nil {
			previousExecutablesTarget = live
		}
	}
	retired, err := prepareExecutableShims(built)
	if err != nil {
		return ReplaceDriftedResult{}, err
	}
	if previousExecutablesTarget != "" && previousExecutablesTarget != built.ExecutablesTarget {
		previous := Result{Consumer: built.Consumer, ExecutablesTarget: previousExecutablesTarget}
		paths, err := retiredExecutableShims(previous, map[string]executableSpec{})
		if err != nil {
			return ReplaceDriftedResult{}, err
		}
		if err := removeExecutableShims(paths); err != nil {
			return ReplaceDriftedResult{}, err
		}
		if err := removeRetiredExecutableDispatchers(previousExecutablesTarget); err != nil {
			return ReplaceDriftedResult{}, err
		}
	}
	if err := activateGeneration(built.Target, built.Generation, consumerName, false); err != nil {
		return ReplaceDriftedResult{}, err
	}
	if err := removeExecutableShims(retired); err != nil {
		return ReplaceDriftedResult{}, err
	}
	if err := removeRetiredExecutableDispatchers(built.ExecutablesTarget); err != nil {
		return ReplaceDriftedResult{}, err
	}

	verified, err := Verify(repo, ref, consumerName, cache)
	if err != nil {
		return ReplaceDriftedResult{}, fmt.Errorf("post-replace verify: %w", err)
	}
	return ReplaceDriftedResult{
		Result:       built,
		EvidenceDir:  evidencePath,
		Evidence:     evidence,
		Verification: verified,
	}, nil
}

func inspectDriftedActiveTarget(target, consumer string) (generation string, marker Marker, actualHash string, err error) {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return "", Marker{}, "", fmt.Errorf("active target does not exist: %s", target)
	}
	if err != nil {
		return "", Marker{}, "", fmt.Errorf("inspect target: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if info.IsDir() {
			return "", Marker{}, "", fmt.Errorf("active target is an ordinary directory, not an sm projection symlink: %s", target)
		}
		return "", Marker{}, "", fmt.Errorf("active target is not an sm projection symlink: %s", target)
	}
	generation, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", Marker{}, "", fmt.Errorf("existing target is a broken symlink: %s", target)
	}
	marker, actualHash, err = inspectOwnedProjection(generation, consumer)
	if err != nil {
		return "", Marker{}, "", fmt.Errorf("existing target is not owned by sm: %w", err)
	}
	return generation, marker, actualHash, nil
}

func preserveGeneration(generation, timestamp string) (string, error) {
	slug := sanitizeTimestampSlug(timestamp)
	preserved := generation + ".replaced-" + slug
	if _, err := os.Lstat(preserved); err == nil {
		return "", fmt.Errorf("preserved generation already exists: %s", preserved)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(generation, preserved); err != nil {
		return "", fmt.Errorf("preserve drifted generation: %w", err)
	}
	return preserved, nil
}

func sanitizeTimestampSlug(timestamp string) string {
	replacer := strings.NewReplacer(":", "-", "+", "-")
	return replacer.Replace(timestamp)
}

func writeReplaceDriftedEvidence(destination string, destinationExists bool, evidence ReplaceDriftedEvidence) error {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create evidence parent: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".sm-replace-drifted-")
	if err != nil {
		return fmt.Errorf("create evidence staging directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(staging)
		}
	}()
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(staging, replaceDriftedEvidenceName), data, 0o644); err != nil {
		return err
	}
	if destinationExists {
		if err := os.Remove(destination); err != nil {
			return fmt.Errorf("replace empty evidence destination: %w", err)
		}
	}
	if err := os.Rename(staging, destination); err != nil {
		return fmt.Errorf("publish evidence: %w", err)
	}
	keep = true
	return nil
}

func driftedProjectionChanges(repo, drifted string, marker Marker) ([]TreeChange, error) {
	expectedRoot, cleanup, err := materializeExpectedProjection(repo, marker)
	if err != nil {
		// Path-level diff is best-effort; hash mismatch remains in evidence.
		return nil, nil
	}
	defer cleanup()
	return diffProjectionTrees(expectedRoot, drifted)
}

func materializeExpectedProjection(repo string, marker Marker) (string, func(), error) {
	consumer, err := loadConsumer(repo, marker.Commit, marker.Consumer)
	if err != nil {
		return "", nil, err
	}
	parent, err := os.MkdirTemp("", ".sm-expected-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(parent) }
	keep := false
	defer func() {
		if !keep {
			cleanup()
		}
	}()
	if err := extractSkills(repo, marker.Commit, consumer.Skills, parent); err != nil {
		return "", nil, err
	}
	if err := prepareExecutableProjection(parent, consumer.Skills); err != nil {
		return "", nil, err
	}
	if err := prepareAdapterArtifact(parent, marker.Consumer, consumer); err != nil {
		return "", nil, err
	}
	keep = true
	return parent, cleanup, nil
}

func diffProjectionTrees(expectedRoot, actualRoot string) ([]TreeChange, error) {
	expected, err := projectionEntryIndex(expectedRoot)
	if err != nil {
		return nil, err
	}
	actual, err := projectionEntryIndex(actualRoot)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]struct{}, len(expected)+len(actual))
	for path := range expected {
		paths[path] = struct{}{}
	}
	for path := range actual {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	var changes []TreeChange
	for _, path := range ordered {
		want, haveWant := expected[path]
		got, haveGot := actual[path]
		switch {
		case !haveWant:
			changes = append(changes, TreeChange{Path: path, Type: "added"})
		case !haveGot:
			changes = append(changes, TreeChange{Path: path, Type: "removed"})
		case want.Kind != got.Kind:
			changes = append(changes, TreeChange{Path: path, Type: "type-changed"})
		case want.Kind == "file" && (want.Executable != got.Executable || want.Hash != got.Hash):
			changes = append(changes, TreeChange{Path: path, Type: "modified"})
		}
	}
	return changes, nil
}

type projectionEntry struct {
	Kind       string
	Executable bool
	Hash       string
}

func projectionEntryIndex(root string) (map[string]projectionEntry, error) {
	index := make(map[string]projectionEntry)
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if relative == "." || relative == markerName {
			return nil
		}
		relative = filepath.ToSlash(relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			index[relative] = projectionEntry{Kind: "dir"}
		case info.Mode().IsRegular():
			hash, err := fileSHA256(name)
			if err != nil {
				return err
			}
			index[relative] = projectionEntry{
				Kind:       "file",
				Executable: info.Mode()&0o111 != 0,
				Hash:       hash,
			}
		default:
			return fmt.Errorf("projection contains unsupported entry: %s", relative)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return index, nil
}

func samePath(left, right string) bool {
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil && rightErr == nil {
		return leftResolved == rightResolved
	}
	leftAbs, leftAbsErr := filepath.Abs(left)
	rightAbs, rightAbsErr := filepath.Abs(right)
	if leftAbsErr != nil || rightAbsErr != nil {
		return left == right
	}
	return leftAbs == rightAbs
}
