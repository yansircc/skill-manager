package skillmanager

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookupAgentAdapter(t *testing.T) {
	for _, name := range []string{"directory", "codex", "claude", "pi"} {
		adapter, err := lookupAgentAdapter(name)
		if err != nil {
			t.Fatalf("lookupAgentAdapter(%q) = %v", name, err)
		}
		if adapter.Name() != name {
			t.Fatalf("adapter name = %q, want %q", adapter.Name(), name)
		}
	}
	if _, err := lookupAgentAdapter("unknown"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("lookupAgentAdapter(unknown) = %v, want unsupported", err)
	}
	if !agentAdapters["directory"].Persistent() || !agentAdapters["codex"].Persistent() {
		t.Fatal("directory and codex must be persistent")
	}
	if agentAdapters["claude"].Persistent() || agentAdapters["pi"].Persistent() {
		t.Fatal("claude and pi must be ephemeral")
	}
}

func TestCodexVerifyEmitsAgentAPIEvidence(t *testing.T) {
	repo := newTestRepository(t)
	target := filepath.Join(t.TempDir(), "skills")
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "codex", Target: target, Skills: []string{"alpha"}})
	commitAll(t, repo, "codex consumer")
	result, err := Apply(repo, "HEAD", "codex.global", cache)
	if err != nil {
		t.Fatal(err)
	}

	original := listCodexSkills
	t.Cleanup(func() { listCodexSkills = original })
	listCodexSkills = func(string) ([]CodexSkill, error) {
		return []CodexSkill{
			{Name: "alpha", Path: filepath.Join(result.Generation, "alpha", "SKILL.md"), Scope: "user", Enabled: true},
			{Name: "foreign", Path: filepath.Join(t.TempDir(), "foreign", "SKILL.md"), Scope: "repo", Enabled: true},
		}, nil
	}
	verification, err := VerifyMode(repo, "HEAD", "codex.global", cache, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(verification.ExternalSkills) != 1 {
		t.Fatalf("ExternalSkills = %#v", verification.ExternalSkills)
	}
	if verification.Evidence == nil || verification.Evidence.Kind != ProofKindAgentAPI {
		t.Fatalf("evidence = %#v, want agent-api", verification.Evidence)
	}
	if len(verification.Evidence.Discovery) != 2 {
		t.Fatalf("discovery = %#v", verification.Evidence.Discovery)
	}
	for _, record := range verification.Evidence.Discovery {
		if record.Source != "codex" || record.Name == "" || record.Path == "" {
			t.Fatalf("discovery record = %#v", record)
		}
	}
}

func TestClaudeClosedVerifyFilesystemGuard(t *testing.T) {
	repo := newTestRepository(t)
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "claude.global", Consumer{Adapter: "claude", Skills: []string{"alpha"}})
	commitAll(t, repo, "claude consumer")
	if _, err := Build(repo, "HEAD", "claude.global", cache); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(repo, "HEAD", "claude.global", cache); err == nil || !strings.Contains(err.Error(), "sm exec") {
		t.Fatalf("Verify error = %v, want ephemeral refusal", err)
	}
	verification, err := VerifyMode(repo, "HEAD", "claude.global", cache, true)
	if err != nil {
		t.Fatal(err)
	}
	if verification.Evidence == nil || verification.Evidence.Kind != ProofKindFilesystemGuard {
		t.Fatalf("evidence = %#v, want filesystem-guard", verification.Evidence)
	}
	if len(verification.Evidence.Discovery) != 0 {
		t.Fatalf("closed Claude discovery = %#v, want empty", verification.Evidence.Discovery)
	}
}

func TestClaudeClosedVerifyRejectsProfileSkill(t *testing.T) {
	repo := newTestRepository(t)
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "claude.global", Consumer{Adapter: "claude", Skills: []string{"alpha"}})
	commitAll(t, repo, "claude consumer")
	built, err := Build(repo, "HEAD", "claude.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := prepareClaudeProfile(built.Generation, built.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(profile, "skills", "foreign", "SKILL.md"), "foreign")
	if _, err := VerifyMode(repo, "HEAD", "claude.global", cache, true); err == nil || !strings.Contains(err.Error(), "claude profile") {
		t.Fatalf("VerifyMode error = %v, want profile guard failure", err)
	}
}

func TestPiClosedVerifyLaunchClosure(t *testing.T) {
	repo := newTestRepository(t)
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{"alpha"}})
	commitAll(t, repo, "pi consumer")
	built, err := Build(repo, "HEAD", "pi.global", cache)
	if err != nil {
		t.Fatal(err)
	}
	verification, err := VerifyMode(repo, "HEAD", "pi.global", cache, true)
	if err != nil {
		t.Fatal(err)
	}
	if verification.Evidence == nil || verification.Evidence.Kind != ProofKindLaunchClosure {
		t.Fatalf("evidence = %#v, want launch-closure", verification.Evidence)
	}
	want := piLaunchArguments(built.Generation)
	if strings.Join(verification.Evidence.Command, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command = %#v, want %#v", verification.Evidence.Command, want)
	}
}

func TestPiLaunchClosureRejectsDiscoveryReintroduction(t *testing.T) {
	generation := "/tmp/generation"
	arguments := append(piLaunchArguments(generation), "--skill=/tmp/other")
	if err := validatePiLaunchClosure(arguments, generation); err == nil || !strings.Contains(err.Error(), "reintroduce") {
		t.Fatalf("validatePiLaunchClosure error = %v, want reintroduction refusal", err)
	}
	if err := validatePiLaunchClosure([]string{"--skill", generation}, generation); err == nil || !strings.Contains(err.Error(), "missing discovery-disabling") {
		t.Fatalf("validatePiLaunchClosure error = %v, want missing disable flags", err)
	}
}

func TestVerifyJSONIncludesEvidence(t *testing.T) {
	repo := newTestRepository(t)
	cache := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { _ = makeWritable(cache) })
	writeSkill(t, repo, "alpha", "alpha")
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{"alpha"}})
	commitAll(t, repo, "pi consumer")
	if _, err := Build(repo, "HEAD", "pi.global", cache); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if err := RunCLI([]string{
		"verify", "--repo", repo, "--cache", cache, "--closed", "--json", "pi.global",
	}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var verification Verification
	if err := json.Unmarshal([]byte(stdout.String()), &verification); err != nil {
		t.Fatalf("decode verification JSON: %v\n%s", err, stdout.String())
	}
	if verification.Evidence == nil || verification.Evidence.Kind != ProofKindLaunchClosure {
		t.Fatalf("JSON evidence = %#v", verification.Evidence)
	}
	if verification.Result.Consumer != "pi.global" {
		t.Fatalf("JSON result = %#v", verification.Result)
	}
}
