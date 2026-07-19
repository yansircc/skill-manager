package skillmanager

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type executableSpec struct {
	SkillID string
	Path    string
}

type executableShimMarker struct {
	Protocol     string `json:"protocol"`
	Consumer     string `json:"consumer"`
	Command      string `json:"command"`
	Projection   string `json:"projection"`
	Target       string `json:"target"`
	LauncherHash string `json:"launcherHash"`
}

const executableMetadataSuffix = ".sm-executable.json"

func validateDeclaredExecutables(root string, metadata SkillMetadata) error {
	for command, relative := range metadata.Executables {
		path := filepath.Join(root, relative)
		if !within(root, path) {
			return fmt.Errorf("executable %q path escapes the skill", command)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("executable %q: %w", command, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			return fmt.Errorf("executable %q is not an executable regular file: %s", command, relative)
		}
	}
	return nil
}

func prepareExecutableProjection(root string, skills []string) error {
	owners := make(map[string]string)
	for _, skill := range skills {
		skillRoot := filepath.Join(root, skill)
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
		commands := make([]string, 0, len(metadata.Executables))
		for command := range metadata.Executables {
			commands = append(commands, command)
		}
		sort.Strings(commands)
		for _, command := range commands {
			key := executableCommandKey(command)
			if owner, exists := owners[key]; exists {
				return fmt.Errorf("executable %q is declared by both %s and %s", command, owner, skill)
			}
			owners[key] = skill
			directory := filepath.Join(root, executablesDir)
			if err := os.MkdirAll(directory, 0o755); err != nil {
				return err
			}
			if err := os.Link(filepath.Join(skillRoot, metadata.Executables[command]), filepath.Join(directory, command)); err != nil {
				return fmt.Errorf("project executable %q from skill %q: %w", command, skill, err)
			}
		}
	}
	return nil
}

func generationExecutables(generation string) (map[string]executableSpec, error) {
	entries, err := os.ReadDir(generation)
	if err != nil {
		return nil, err
	}
	result := make(map[string]executableSpec)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == executablesDir {
			continue
		}
		root := filepath.Join(generation, entry.Name())
		if _, err := os.Stat(filepath.Join(root, "SKILL.md")); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		metadata, present, err := readOptionalSkillMetadata(root)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		for command, relative := range metadata.Executables {
			key := executableCommandKey(command)
			if existing, exists := result[key]; exists {
				return nil, fmt.Errorf("duplicate projected executable %q conflicts with skill %q", command, existing.SkillID)
			}
			result[key] = executableSpec{SkillID: entry.Name(), Path: relative}
		}
	}
	return result, nil
}

func hashExecutableProjection(generation string) (map[string]string, error) {
	directory := filepath.Join(generation, executablesDir)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	hashes := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			return nil, fmt.Errorf("executable projection contains a directory: %s", entry.Name())
		}
		hash, err := fileSHA256(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		hashes[executableCommandKey(entry.Name())] = hash
	}
	return hashes, nil
}

func prepareExecutableShims(result Result) ([]string, error) {
	executables, err := generationExecutables(result.Generation)
	if err != nil {
		return nil, err
	}
	if len(executables) != 0 && result.ExecutablesTarget == "" {
		return nil, fmt.Errorf("consumer %q authorizes executables but has no executablesTarget", result.Consumer)
	}
	if result.ExecutablesTarget == "" {
		return nil, nil
	}
	if err := os.MkdirAll(result.ExecutablesTarget, 0o755); err != nil {
		return nil, err
	}
	dispatcher, err := prepareExecutableDispatcher(result.ExecutablesTarget)
	if err != nil {
		return nil, err
	}
	commands := make([]string, 0, len(executables))
	for command := range executables {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	for _, command := range commands {
		path := executableLauncherPath(result.ExecutablesTarget, command)
		if marker, ok, err := readExecutableShimMarker(path); err != nil {
			return nil, err
		} else if ok && marker.Consumer != result.Consumer {
			return nil, fmt.Errorf("executable target %s is owned by consumer %q", path, marker.Consumer)
		} else if ok {
			if _, err := os.Lstat(path); err == nil {
				hash, err := fileSHA256(path)
				if err != nil {
					return nil, err
				}
				if hash != marker.LauncherHash {
					return nil, fmt.Errorf("refusing to replace executable target with an invalid managed launcher: %s", path)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		} else if !ok {
			if _, err := os.Lstat(path); err == nil {
				return nil, fmt.Errorf("refusing to replace unmanaged executable target: %s", path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		metadata, err := executableShim(result, command, executables[command])
		if err != nil {
			return nil, err
		}
		if err := writeFileAtomic(executableMetadataPath(path), metadata, 0o444); err != nil {
			return nil, err
		}
		if err := writeExecutableLauncherAtomic(path, dispatcher); err != nil {
			return nil, err
		}
	}
	return retiredExecutableShims(result, executables)
}

func executableShim(result Result, command string, spec executableSpec) ([]byte, error) {
	launcherHash, err := currentExecutableHash()
	if err != nil {
		return nil, err
	}
	marker, err := json.MarshalIndent(executableShimMarker{
		Protocol:     shimProtocol,
		Consumer:     result.Consumer,
		Command:      command,
		Projection:   result.Target,
		Target:       filepath.Join(result.Target, spec.SkillID, spec.Path),
		LauncherHash: launcherHash,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(marker, '\n'), nil
}

func executableLauncherPath(directory, command string) string {
	name := command
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		name += ".exe"
	}
	return filepath.Join(directory, name)
}

func executableCommandKey(command string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(command)
	}
	return command
}

func executableMetadataPath(launcher string) string {
	return filepath.Join(filepath.Dir(launcher), "."+filepath.Base(launcher)+executableMetadataSuffix)
}

func executableDispatcherPath(directory, hash string) string {
	name := ".sm-executable-dispatch-" + hash
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(directory, name)
}

func prepareExecutableDispatcher(directory string) (string, error) {
	hash, err := currentExecutableHash()
	if err != nil {
		return "", err
	}
	path := executableDispatcherPath(directory, hash)
	if actual, err := fileSHA256(path); err == nil {
		if actual != hash {
			return "", fmt.Errorf("managed executable dispatcher hash mismatch: %s", path)
		}
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := copyCurrentExecutableAtomic(path); err != nil {
		return "", err
	}
	return path, nil
}

func copyCurrentExecutableAtomic(path string) error {
	sourcePath, err := os.Executable()
	if err != nil {
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".sm-new-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o555); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := io.Copy(file, source); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func writeExecutableLauncherAtomic(path, dispatcher string) error {
	temporaryFile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".sm-new-")
	if err != nil {
		return err
	}
	temporary := temporaryFile.Name()
	if err := temporaryFile.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Remove(temporary); err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Link(dispatcher, temporary); err != nil {
		return fmt.Errorf("link executable launcher to managed dispatcher: %w", err)
	}
	return os.Rename(temporary, path)
}

func readExecutableShimMarker(path string) (executableShimMarker, bool, error) {
	file, err := os.Open(executableMetadataPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return executableShimMarker{}, false, nil
	}
	if err != nil {
		return executableShimMarker{}, false, err
	}
	var marker executableShimMarker
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		_ = file.Close()
		return executableShimMarker{}, false, fmt.Errorf("parse executable shim marker %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return executableShimMarker{}, false, err
	}
	if marker.Protocol != shimProtocol || marker.Command == "" || marker.Consumer == "" || marker.Projection == "" || marker.Target == "" || marker.LauncherHash == "" {
		return executableShimMarker{}, false, fmt.Errorf("invalid executable shim marker: %s", executableMetadataPath(path))
	}
	return marker, true, nil
}

func retiredExecutableShims(result Result, desired map[string]executableSpec) ([]string, error) {
	entries, err := os.ReadDir(result.ExecutablesTarget)
	if err != nil {
		return nil, err
	}
	var retired []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), ".") || !strings.HasSuffix(entry.Name(), executableMetadataSuffix) {
			continue
		}
		launcherName := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "."), executableMetadataSuffix)
		path := filepath.Join(result.ExecutablesTarget, launcherName)
		marker, managed, err := readExecutableShimMarker(path)
		if err != nil {
			return nil, err
		}
		if managed && marker.Consumer == result.Consumer {
			if _, exists := desired[marker.Command]; !exists || executableLauncherPath(result.ExecutablesTarget, marker.Command) != path {
				retired = append(retired, path)
			}
		}
	}
	sort.Strings(retired)
	return retired, nil
}

func removeExecutableShims(paths []string) error {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Remove(executableMetadataPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func removeRetiredExecutableDispatchers(directory string) error {
	if directory == "" {
		return nil
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	currentHash, err := currentExecutableHash()
	if err != nil {
		return err
	}
	hasManagedLaunchers := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), executableMetadataSuffix) {
			launcherName := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "."), executableMetadataSuffix)
			_, managed, err := readExecutableShimMarker(filepath.Join(directory, launcherName))
			if err != nil {
				return err
			}
			if managed {
				hasManagedLaunchers = true
				break
			}
		}
	}
	prefix := ".sm-executable-dispatch-"
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		encodedHash := strings.TrimPrefix(entry.Name(), prefix)
		if runtime.GOOS == "windows" {
			encodedHash = strings.TrimSuffix(strings.ToLower(encodedHash), ".exe")
		}
		path := filepath.Join(directory, entry.Name())
		actualHash, err := fileSHA256(path)
		if err != nil {
			return err
		}
		if actualHash != encodedHash {
			return fmt.Errorf("refusing to remove invalid managed executable dispatcher: %s", path)
		}
		if encodedHash == currentHash && hasManagedLaunchers {
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func verifyExecutableShims(result Result) error {
	executables, err := generationExecutables(result.Generation)
	if err != nil {
		return err
	}
	if len(executables) != 0 && result.ExecutablesTarget == "" {
		return fmt.Errorf("consumer %q authorizes executables but has no executablesTarget", result.Consumer)
	}
	if result.ExecutablesTarget == "" {
		return nil
	}
	retired, err := retiredExecutableShims(result, executables)
	if err != nil {
		return err
	}
	if len(retired) != 0 {
		return fmt.Errorf("retired executable shim remains active: %s", retired[0])
	}
	commands := make([]string, 0, len(executables))
	for command := range executables {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	for _, command := range commands {
		path := executableLauncherPath(result.ExecutablesTarget, command)
		actual, err := os.ReadFile(executableMetadataPath(path))
		if err != nil {
			return fmt.Errorf("read executable metadata %q: %w", command, err)
		}
		expected, err := executableShim(result, command, executables[command])
		if err != nil {
			return err
		}
		if string(actual) != string(expected) {
			return fmt.Errorf("executable metadata %q drifted from the active skill projection", command)
		}
		matches, err := launcherMatchesCurrentExecutable(path)
		if err != nil {
			return err
		}
		if !matches {
			return fmt.Errorf("executable launcher %q does not match the active sm compiler", command)
		}
		resolved, err := exec.LookPath(command)
		if err != nil {
			return fmt.Errorf("executable %q is not on PATH: %w", command, err)
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return err
		}
		if filepath.Clean(resolved) != filepath.Clean(path) {
			return fmt.Errorf("executable %q resolves to %s, want %s", command, resolved, path)
		}
	}
	return nil
}

func launcherMatchesCurrentExecutable(path string) (bool, error) {
	want, err := currentExecutableHash()
	if err != nil {
		return false, err
	}
	got, err := fileSHA256(path)
	return got == want, err
}

func currentExecutableHash() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return fileSHA256(path)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func DetectManagedExecutableInvocation(path string) (bool, error) {
	_, managed, err := readExecutableShimMarker(path)
	return managed, err
}

func RunManagedExecutable(path string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	marker, managed, err := readExecutableShimMarker(path)
	if err != nil {
		return err
	}
	if !managed {
		return fmt.Errorf("executable launcher is not managed by sm: %s", path)
	}
	name := filepath.Base(path)
	expectedName := filepath.Base(executableLauncherPath("", marker.Command))
	if runtime.GOOS == "windows" {
		name = strings.ToLower(name)
		expectedName = strings.ToLower(expectedName)
	}
	if name != expectedName {
		return fmt.Errorf("executable launcher name %q does not match managed command %q", name, marker.Command)
	}
	launcherHash, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if launcherHash != marker.LauncherHash {
		return fmt.Errorf("executable launcher bytes do not match managed metadata: %s", path)
	}
	info, err := os.Lstat(marker.Projection)
	if err != nil {
		return fmt.Errorf("inspect active executable projection: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("active executable projection is not an sm symlink: %s", marker.Projection)
	}
	generation, err := filepath.EvalSymlinks(marker.Projection)
	if err != nil {
		return err
	}
	generationInfo, err := os.Lstat(generation)
	if err != nil {
		return err
	}
	if !generationInfo.IsDir() || generationInfo.Mode()&os.ModeSymlink != 0 || generationInfo.Mode().Perm()&0o222 != 0 {
		return fmt.Errorf("active executable generation is not a read-only real directory: %s", generation)
	}
	generationMarker, err := readGenerationMarker(generation)
	if err != nil {
		return err
	}
	if generationMarker.Schema != 1 || generationMarker.Consumer != marker.Consumer || generationMarker.Commit == "" || generationMarker.Compiler == "" || generationMarker.TreeHash == "" {
		return fmt.Errorf("active executable generation marker does not match consumer %q", marker.Consumer)
	}
	markerInfo, err := os.Stat(filepath.Join(generation, markerName))
	if err != nil {
		return err
	}
	if markerInfo.Mode().Perm()&0o222 != 0 {
		return fmt.Errorf("active executable generation marker is writable: %s", filepath.Join(generation, markerName))
	}
	target, err := filepath.EvalSymlinks(marker.Target)
	if err != nil {
		return fmt.Errorf("resolve managed executable target: %w", err)
	}
	if !within(generation, target) {
		return fmt.Errorf("managed executable target escapes the active generation: %s", target)
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !targetInfo.Mode().IsRegular() || targetInfo.Mode()&0o111 == 0 {
		return fmt.Errorf("managed executable target is not executable: %s", target)
	}
	if targetInfo.Mode().Perm()&0o222 != 0 {
		return fmt.Errorf("managed executable target is writable: %s", target)
	}
	expectedHash, ok := generationMarker.Executables[executableCommandKey(marker.Command)]
	if !ok {
		return fmt.Errorf("managed command %q is absent from the active generation marker", marker.Command)
	}
	actualHash, err := fileSHA256(target)
	if err != nil {
		return err
	}
	if actualHash != expectedHash {
		return fmt.Errorf("managed executable target hash mismatch: %s", target)
	}
	command := exec.Command(target, args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	command.Env = os.Environ()
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return &ProcessExitError{Code: exitError.ExitCode()}
		}
		return err
	}
	return nil
}

func prependPath(environment []string, directory string) []string {
	path := os.Getenv("PATH")
	for _, entry := range environment {
		if strings.HasPrefix(entry, "PATH=") {
			path = strings.TrimPrefix(entry, "PATH=")
			break
		}
	}
	return replaceEnvironment(environment, "PATH", directory+string(os.PathListSeparator)+path)
}
