package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const (
	compilerProtocol = "directory-v1"
	markerName       = ".sm-projection.json"
)

type Consumer struct {
	Adapter string   `json:"adapter"`
	Target  string   `json:"target"`
	Skills  []string `json:"skills"`
}

type Marker struct {
	Schema   int    `json:"schema"`
	Commit   string `json:"commit"`
	Consumer string `json:"consumer"`
	Compiler string `json:"compiler"`
	TreeHash string `json:"treeHash"`
}

type Result struct {
	Commit     string
	Consumer   string
	Adapter    string
	Generation string
	Target     string
}

type ProcessExitError struct {
	Code int
}

func (err *ProcessExitError) Error() string {
	return fmt.Sprintf("agent exited with status %d", err.Code)
}

func Init(location string) (string, error) {
	root, err := filepath.Abs(location)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		return "", fmt.Errorf("create skills directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "consumers"), 0o755); err != nil {
		return "", fmt.Errorf("create consumers directory: %w", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := runGit(root, "init"); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	return root, nil
}

func Adopt(repo, source, id string) (string, error) {
	root, err := repositoryRoot(repo)
	if err != nil {
		return "", err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("inspect source: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("source is not a directory: %s", source)
	}
	if id == "" {
		id = filepath.Base(source)
	}
	if err := validateID(id); err != nil {
		return "", err
	}
	if err := validateCanonicalSkill(source); err != nil {
		return "", err
	}
	destination := filepath.Join(root, "skills", id)
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return "", fmt.Errorf("skill already exists: %s", id)
		}
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(source, destination); err != nil {
		return "", fmt.Errorf("move skill into SSOT: %w; adopt requires source and SSOT on the same filesystem", err)
	}
	return destination, nil
}

func Build(repo, ref, consumerName, cache string) (Result, error) {
	root, err := repositoryRoot(repo)
	if err != nil {
		return Result{}, err
	}
	commit, err := resolveCommit(root, ref)
	if err != nil {
		return Result{}, err
	}
	consumer, err := loadConsumer(root, commit, consumerName)
	if err != nil {
		return Result{}, err
	}
	cache, err = cacheRoot(cache)
	if err != nil {
		return Result{}, err
	}
	compiler, err := compilerIdentity()
	if err != nil {
		return Result{}, err
	}
	key := generationKey(commit, consumerName, compiler)
	generation := filepath.Join(cache, "generations", key)
	expected := Marker{Schema: 1, Commit: commit, Consumer: consumerName, Compiler: compiler}

	if _, err := os.Lstat(generation); err == nil {
		if err := validateGeneration(generation, expected); err != nil {
			return Result{}, fmt.Errorf("generation cache is corrupt: %w", err)
		}
		return result(commit, consumerName, generation, consumer), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}

	parent := filepath.Dir(generation)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Result{}, err
	}
	tmp, err := os.MkdirTemp(parent, ".sm-build-")
	if err != nil {
		return Result{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = makeWritable(tmp)
			_ = os.RemoveAll(tmp)
		}
	}()

	if err := extractSkills(root, commit, consumer.Skills, tmp); err != nil {
		return Result{}, err
	}
	if err := prepareAdapterArtifact(tmp, consumerName, consumer); err != nil {
		return Result{}, err
	}
	treeHash, err := hashTree(tmp)
	if err != nil {
		return Result{}, err
	}
	expected.TreeHash = treeHash
	markerBytes, err := json.MarshalIndent(expected, "", "  ")
	if err != nil {
		return Result{}, err
	}
	markerBytes = append(markerBytes, '\n')
	if err := os.WriteFile(filepath.Join(tmp, markerName), markerBytes, 0o644); err != nil {
		return Result{}, err
	}
	if err := makeReadOnly(tmp); err != nil {
		return Result{}, err
	}
	if err := os.Rename(tmp, generation); err != nil {
		if _, statErr := os.Lstat(generation); statErr == nil {
			if validateErr := validateGeneration(generation, expected); validateErr == nil {
				return result(commit, consumerName, generation, consumer), nil
			}
		}
		return Result{}, fmt.Errorf("publish generation: %w", err)
	}
	keep = true
	return result(commit, consumerName, generation, consumer), nil
}

func Apply(repo, ref, consumerName, cache string) (Result, error) {
	built, err := Build(repo, ref, consumerName, cache)
	if err != nil {
		return Result{}, err
	}
	if built.Adapter != "directory" && built.Adapter != "codex" {
		return Result{}, fmt.Errorf("consumer %q uses %s activation; use sm exec", consumerName, built.Adapter)
	}
	if err := activate(built.Target, built.Generation, consumerName); err != nil {
		return Result{}, err
	}
	return built, nil
}

func Verify(repo, ref, consumerName, cache string) (Result, error) {
	root, err := repositoryRoot(repo)
	if err != nil {
		return Result{}, err
	}
	commit, err := resolveCommit(root, ref)
	if err != nil {
		return Result{}, err
	}
	consumer, err := loadConsumer(root, commit, consumerName)
	if err != nil {
		return Result{}, err
	}
	cache, err = cacheRoot(cache)
	if err != nil {
		return Result{}, err
	}
	compiler, err := compilerIdentity()
	if err != nil {
		return Result{}, err
	}
	generation := filepath.Join(cache, "generations", generationKey(commit, consumerName, compiler))
	expected := Marker{Schema: 1, Commit: commit, Consumer: consumerName, Compiler: compiler}
	if err := validateGeneration(generation, expected); err != nil {
		return Result{}, err
	}
	if consumer.Adapter != "directory" && consumer.Adapter != "codex" {
		return Result{}, fmt.Errorf("consumer %q uses ephemeral %s activation; use sm exec", consumerName, consumer.Adapter)
	}
	target, err := expandHome(consumer.Target)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return Result{}, fmt.Errorf("inspect target: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return Result{}, fmt.Errorf("target is not an sm projection: %s", target)
	}
	actual, err := filepath.EvalSymlinks(target)
	if err != nil {
		return Result{}, fmt.Errorf("resolve target: %w", err)
	}
	want, err := filepath.EvalSymlinks(generation)
	if err != nil {
		return Result{}, fmt.Errorf("resolve generation: %w", err)
	}
	if actual != want {
		return Result{}, fmt.Errorf("target drift: points to %s, want %s", actual, want)
	}
	if consumer.Adapter == "codex" {
		cwd, err := os.Getwd()
		if err != nil {
			return Result{}, err
		}
		skills, err := listCodexSkills(cwd)
		if err != nil {
			return Result{}, fmt.Errorf("inspect Codex discovery closure: %w", err)
		}
		if err := validateCodexClosure(skills, generation); err != nil {
			return Result{}, err
		}
	}
	return result(commit, consumerName, generation, consumer), nil
}

func result(commit, consumerName, generation string, consumer Consumer) Result {
	target := ""
	if consumer.Target != "" {
		target, _ = expandHome(consumer.Target)
	}
	return Result{Commit: commit, Consumer: consumerName, Adapter: consumer.Adapter, Generation: generation, Target: target}
}

func repositoryRoot(location string) (string, error) {
	abs, err := filepath.Abs(location)
	if err != nil {
		return "", err
	}
	out, err := runGit(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("find SSOT repository: %w", err)
	}
	return filepath.Clean(strings.TrimSpace(out)), nil
}

func resolveCommit(repo, ref string) (string, error) {
	if strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("invalid Git ref %q", ref)
	}
	out, err := runGit(repo, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve published commit %q: %w", ref, err)
	}
	return strings.TrimSpace(out), nil
}

func loadConsumer(repo, commit, name string) (Consumer, error) {
	if err := validateID(name); err != nil {
		return Consumer{}, fmt.Errorf("invalid consumer: %w", err)
	}
	out, err := runGit(repo, "show", commit+":consumers/"+name+".json")
	if err != nil {
		return Consumer{}, fmt.Errorf("load consumer %q from commit: %w", name, err)
	}
	decoder := json.NewDecoder(strings.NewReader(out))
	decoder.DisallowUnknownFields()
	var consumer Consumer
	if err := decoder.Decode(&consumer); err != nil {
		return Consumer{}, fmt.Errorf("parse consumer %q: %w", name, err)
	}
	if err := validateConsumer(consumer); err != nil {
		return Consumer{}, fmt.Errorf("consumer %q: %w", name, err)
	}
	return consumer, nil
}

func validateConsumer(consumer Consumer) error {
	if consumer.Adapter != "directory" && consumer.Adapter != "codex" && consumer.Adapter != "pi" && consumer.Adapter != "claude" {
		return fmt.Errorf("unsupported adapter %q", consumer.Adapter)
	}
	if consumer.Adapter == "directory" || consumer.Adapter == "codex" {
		if consumer.Target == "" {
			return fmt.Errorf("target is required")
		}
		if consumer.Target != "~" && !strings.HasPrefix(consumer.Target, "~/") && !filepath.IsAbs(consumer.Target) {
			return fmt.Errorf("target must be absolute or start with ~/")
		}
	} else if consumer.Target != "" {
		return fmt.Errorf("target is invalid for %s adapter", consumer.Adapter)
	}
	seen := make(map[string]struct{}, len(consumer.Skills))
	for _, id := range consumer.Skills {
		if err := validateID(id); err != nil {
			return err
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate skill %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func AgentCommand(repo, ref, consumerName, cache string, agentArgs []string) (Result, *exec.Cmd, error) {
	if err := rejectSkillExpansionArguments(agentArgs); err != nil {
		return Result{}, nil, err
	}
	built, err := Build(repo, ref, consumerName, cache)
	if err != nil {
		return Result{}, nil, err
	}
	switch built.Adapter {
	case "pi":
		return piAgentCommand(built, agentArgs)
	case "claude":
		return claudeAgentCommand(built, agentArgs)
	case "codex":
		return codexAgentCommand(built, agentArgs)
	default:
		return Result{}, nil, fmt.Errorf("consumer %q adapter %q has no exec contract", consumerName, built.Adapter)
	}
}

func rejectSkillExpansionArguments(arguments []string) error {
	for _, argument := range arguments {
		if argument == "--skill" || strings.HasPrefix(argument, "--skill=") ||
			argument == "--extension" || strings.HasPrefix(argument, "--extension=") || argument == "-e" ||
			argument == "--plugin-dir" || strings.HasPrefix(argument, "--plugin-dir=") ||
			argument == "--plugin-url" || strings.HasPrefix(argument, "--plugin-url=") ||
			argument == "--settings" || strings.HasPrefix(argument, "--settings=") ||
			argument == "--setting-sources" || strings.HasPrefix(argument, "--setting-sources=") ||
			argument == "--add-dir" || strings.HasPrefix(argument, "--add-dir=") {
			return fmt.Errorf("agent argument %q can expand or replace the authorized skill set", argument)
		}
	}
	return nil
}

var findExecutable = exec.LookPath

func piAgentCommand(built Result, agentArgs []string) (Result, *exec.Cmd, error) {
	binary, err := findExecutable("pi")
	if err != nil {
		return Result{}, nil, fmt.Errorf("find pi executable: %w", err)
	}
	arguments := []string{"--no-extensions", "--no-skills", "--skill", built.Generation}
	arguments = append(arguments, agentArgs...)
	return built, exec.Command(binary, arguments...), nil
}

func prepareAdapterArtifact(root, consumerName string, consumer Consumer) error {
	if consumer.Adapter != "claude" {
		return nil
	}
	skillsRoot := filepath.Join(root, "skills")
	if err := os.Mkdir(skillsRoot, 0o755); err != nil {
		return err
	}
	for _, id := range consumer.Skills {
		if err := os.Rename(filepath.Join(root, id), filepath.Join(skillsRoot, id)); err != nil {
			return fmt.Errorf("shape Claude skill %q: %w", id, err)
		}
	}
	manifestRoot := filepath.Join(root, ".claude-plugin")
	if err := os.Mkdir(manifestRoot, 0o755); err != nil {
		return err
	}
	pluginName := strings.ToLower(consumerName)
	pluginName = strings.NewReplacer(".", "-", "_", "-").Replace(pluginName)
	manifest := map[string]string{
		"name":        "sm-" + pluginName,
		"version":     "1.0.0",
		"description": "Immutable sm projection for " + consumerName,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(manifestRoot, "plugin.json"), data, 0o644)
}

func RunConsumer(repo, ref, consumerName, cache string, agentArgs []string, stdin io.Reader, stdout, stderr io.Writer) error {
	_, command, err := AgentCommand(repo, ref, consumerName, cache, agentArgs)
	if err != nil {
		return err
	}
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return &ProcessExitError{Code: exitError.ExitCode()}
		}
		return err
	}
	return nil
}

func validateID(id string) error {
	if id == "" {
		return fmt.Errorf("id is empty")
	}
	for i, r := range id {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if !valid || i == 0 && (r == '.' || r == '_' || r == '-') {
			return fmt.Errorf("invalid id %q", id)
		}
	}
	return nil
}

func validateCanonicalSkill(root string) error {
	info, err := os.Lstat(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return fmt.Errorf("skill requires SKILL.md: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("SKILL.md is not a regular file")
	}
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill contains symlink: %s", name)
		}
		if !entry.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("skill contains unsupported file: %s", name)
		}
		if name != filepath.Join(root, "SKILL.md") && entry.Name() == "SKILL.md" {
			return fmt.Errorf("skill contains nested SKILL.md: %s", name)
		}
		return nil
	})
}

func extractSkills(repo, commit string, skills []string, destination string) error {
	if len(skills) == 0 {
		return nil
	}
	args := []string{"archive", "--format=tar", commit, "--"}
	for _, id := range skills {
		args = append(args, "skills/"+id)
	}
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	extractErr := extractTar(stdout, destination, skills)
	if extractErr != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if extractErr != nil {
		return extractErr
	}
	if waitErr != nil {
		return fmt.Errorf("archive skills: %s: %w", strings.TrimSpace(stderr.String()), waitErr)
	}
	for _, id := range skills {
		if err := validateCanonicalSkill(filepath.Join(destination, id)); err != nil {
			return fmt.Errorf("skill %q is invalid in commit %s: %w", id, commit, err)
		}
	}
	return nil
}

func extractTar(reader io.Reader, destination string, selected []string) error {
	allowed := make(map[string]struct{}, len(selected))
	for _, id := range selected {
		allowed[id] = struct{}{}
	}
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read skill archive: %w", err)
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		clean := path.Clean(header.Name)
		if clean == "skills" && header.Typeflag == tar.TypeDir {
			continue
		}
		parts := strings.Split(clean, "/")
		if len(parts) < 2 || parts[0] != "skills" {
			return fmt.Errorf("archive path escapes skills root: %q", header.Name)
		}
		id := parts[1]
		if _, ok := allowed[id]; !ok {
			return fmt.Errorf("archive contains unauthorized skill %q", id)
		}
		relative := strings.TrimPrefix(clean, "skills/")
		if relative == "" || relative == "." || strings.HasPrefix(relative, "../") {
			return fmt.Errorf("invalid archive path %q", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(relative))
		if !within(destination, target) {
			return fmt.Errorf("archive path escapes destination: %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := fs.FileMode(0o644)
			if header.FileInfo().Mode()&0o111 != 0 {
				mode = 0o755
			}
			file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, tarReader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("skill archive contains unsupported entry %q", header.Name)
		}
	}
}

func activate(target, generation, consumer string) error {
	target, err := expandHome(target)
	if err != nil {
		return err
	}
	if err := validateGeneration(generation, Marker{Schema: 1, Consumer: consumer}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(target)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0:
		resolved, resolveErr := filepath.EvalSymlinks(target)
		if resolveErr != nil {
			return fmt.Errorf("existing target is a broken symlink: %s", target)
		}
		if err := validateGeneration(resolved, Marker{Schema: 1, Consumer: consumer}); err != nil {
			return fmt.Errorf("existing target is not owned by sm: %w", err)
		}
	case info.IsDir():
		entries, readErr := os.ReadDir(target)
		if readErr != nil {
			return readErr
		}
		if len(entries) != 0 {
			return fmt.Errorf("refusing to replace unmanaged non-empty target: %s", target)
		}
		if err := os.Remove(target); err != nil {
			return err
		}
	default:
		return fmt.Errorf("refusing to replace unmanaged target: %s", target)
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".sm-new-")
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
	if err := os.Symlink(generation, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("activate generation: %w", err)
	}
	return nil
}

func validateGeneration(root string, expected Marker) error {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect generation: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("generation is not a real directory: %s", root)
	}
	data, err := os.ReadFile(filepath.Join(root, markerName))
	if err != nil {
		return fmt.Errorf("read projection marker: %w", err)
	}
	var actual Marker
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&actual); err != nil {
		return fmt.Errorf("parse projection marker: %w", err)
	}
	if expected.Schema != 0 && actual.Schema != expected.Schema ||
		expected.Commit != "" && actual.Commit != expected.Commit ||
		expected.Consumer != "" && actual.Consumer != expected.Consumer ||
		expected.Compiler != "" && actual.Compiler != expected.Compiler ||
		expected.TreeHash != "" && actual.TreeHash != expected.TreeHash {
		return fmt.Errorf("projection marker does not match requested generation")
	}
	hash, err := hashTree(root)
	if err != nil {
		return err
	}
	if hash != actual.TreeHash {
		return fmt.Errorf("projection content drift: got %s, want %s", hash, actual.TreeHash)
	}
	return assertReadOnly(root)
}

func hashTree(root string) (string, error) {
	hash := sha256.New()
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
			fmt.Fprintf(hash, "d\x00%s\x00", relative)
		case info.Mode().IsRegular():
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
		default:
			return fmt.Errorf("projection contains unsupported entry: %s", relative)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func assertReadOnly(root string) error {
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o222 != 0 {
			return fmt.Errorf("projection entry is writable: %s", name)
		}
		return nil
	})
}

func makeReadOnly(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, name)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o444)
		if info.Mode()&0o111 != 0 {
			mode = 0o555
		}
		return os.Chmod(name, mode)
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Chmod(directories[i], 0o555); err != nil {
			return err
		}
	}
	return nil
}

func makeWritable(root string) error {
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			return os.Chmod(name, 0o755)
		}
		return os.Chmod(name, 0o644)
	})
}

func generationKey(commit, consumer, compiler string) string {
	sum := sha256.Sum256([]byte(commit + "\x00" + consumer + "\x00" + compiler))
	return hex.EncodeToString(sum[:])
}

func compilerIdentity() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate compiler executable: %w", err)
	}
	file, err := os.Open(executable)
	if err != nil {
		return "", fmt.Errorf("open compiler executable: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("hash compiler executable: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return compilerProtocol + ":" + hex.EncodeToString(hash.Sum(nil)), nil
}

func cacheRoot(configured string) (string, error) {
	if configured != "" {
		return expandHome(configured)
	}
	directory, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "sm"), nil
}

func expandHome(name string) (string, error) {
	if name == "~" || strings.HasPrefix(name, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if name == "~" {
			return home, nil
		}
		name = filepath.Join(home, strings.TrimPrefix(name, "~/"))
	}
	return filepath.Abs(name)
}

func within(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func runGit(repo string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(output)), err)
	}
	return string(output), nil
}
