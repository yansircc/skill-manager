package skillmanager

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestDashboardServesRepoRequiresMatchingSSOT(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(DashboardState{Repo: "/tmp/one"})
	}))
	defer server.Close()
	if !dashboardServesRepo(server.URL, "/tmp/one") {
		t.Fatal("matching dashboard was not detected")
	}
	if dashboardServesRepo(server.URL, "/tmp/two") {
		t.Fatal("dashboard for a different SSOT was reused")
	}
}

func TestDisplayPathShortensHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".sm", "skills")
	if got := displayPath(path); got != filepath.Join("~", ".sm", "skills") {
		t.Fatalf("displayPath() = %q", got)
	}
}

func TestDashboardStateAndGrantUseSSOT(t *testing.T) {
	repo := newTestRepository(t)
	if _, err := runGit(repo, "config", "user.name", "sm-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repo, "config", "user.email", "sm-test@example.com"); err != nil {
		t.Fatal(err)
	}
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "Alpha skill")
	producerRoot := t.TempDir()
	writeNamedSkill(t, filepath.Join(producerRoot, "dist", "alpha"), "alpha", "Alpha skill")
	producer := struct {
		Root    string           `json:"root"`
		Note    string           `json:"note,omitempty"`
		Build   ProducerBuild    `json:"build"`
		Outputs []ProducerOutput `json:"outputs"`
		Skills  []string         `json:"skills"`
	}{producerRoot, "中文备注", ProducerBuild{Argv: []string{"make", "skill"}}, []ProducerOutput{{Path: "dist"}}, []string{"alpha"}}
	data, _ := json.Marshal(producer)
	writeFile(t, filepath.Join(repo, "producers", "example.json"), string(data))
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{}})
	commitAll(t, repo, "initial")

	state, err := dashboardState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Skills) != 1 || state.Skills[0].Description != "Alpha skill" {
		t.Fatalf("state = %#v", state)
	}
	if state.Skills[0].Note != "中文备注" {
		t.Fatalf("skill note = %q", state.Skills[0].Note)
	}
	if len(state.Producers) != 1 || len(state.Producers[0].BuildArgv) != 2 || state.Producers[0].BuildArgv[0] != "make" || state.Producers[0].BuildArgv[1] != "skill" {
		t.Fatalf("producer command = %#v", state.Producers)
	}
	if err := setGrant(repo, "alpha", grantRequest{Consumer: "pi.global", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	consumers, err := readConsumers(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(consumers["pi.global"].Skills) != 1 || consumers["pi.global"].Skills[0] != "alpha" {
		t.Fatalf("consumer = %#v", consumers["pi.global"])
	}
	status, err := runGit(repo, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		t.Fatalf("dashboard left dirty SSOT: %s", status)
	}
	if err := os.Mkdir(filepath.Join(repo, "skills", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "skills", "nested-empty", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	state, err = dashboardState(repo)
	if err != nil {
		t.Fatalf("empty filesystem-only directories affected catalog discovery: %v", err)
	}
	if len(state.Skills) != 1 || state.Skills[0].ID != "alpha" {
		t.Fatalf("empty directories became catalog skills: %#v", state.Skills)
	}
	writeFile(t, filepath.Join(repo, "skills", "malformed", "notes.txt"), "not a skill")
	if _, err := dashboardState(repo); err == nil {
		t.Fatal("dashboard accepted a non-empty skill directory without SKILL.md")
	}
}

func TestDashboardStateExcludesPortableConsumers(t *testing.T) {
	repo := newTestRepository(t)
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "Alpha skill")
	codexTarget := filepath.Join(t.TempDir(), "codex-skills")
	portableTarget := filepath.Join(t.TempDir(), "codex-portable-skills")
	writeConsumer(t, repo, "codex.global", Consumer{Adapter: "codex", Target: codexTarget, Skills: []string{"alpha"}})
	writeConsumer(t, repo, "claude.global", Consumer{Adapter: "claude", Skills: []string{}})
	writeConsumer(t, repo, "pi.global", Consumer{Adapter: "pi", Skills: []string{"alpha"}})
	writeConsumer(t, repo, "codex.portable", Consumer{Adapter: "codex", Target: portableTarget, Skills: []string{"alpha"}})
	writeConsumer(t, repo, "claude.portable", Consumer{Adapter: "claude", Skills: []string{"alpha"}})
	writeConsumer(t, repo, "pi.portable", Consumer{Adapter: "pi", Skills: []string{"alpha"}})
	writeDistribution(t, repo, "portable-agents", Distribution{
		Consumers: []string{"codex.portable", "claude.portable", "pi.portable"},
		Platforms: []string{"linux-amd64"},
	})
	commitAll(t, repo, "initial")

	consumers, err := readConsumers(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(consumers) != 6 {
		t.Fatalf("SSOT consumers = %d, want 6 including portable", len(consumers))
	}

	state, err := dashboardState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Agents) != 3 {
		t.Fatalf("agents = %#v, want exactly three global entries", state.Agents)
	}
	wantAgents := []string{"codex.global", "claude.global", "pi.global"}
	for i, want := range wantAgents {
		if state.Agents[i].ID != want {
			t.Fatalf("agents[%d] = %q, want %q (full %#v)", i, state.Agents[i].ID, want, state.Agents)
		}
	}
	if len(state.Skills) != 1 {
		t.Fatalf("skills = %#v", state.Skills)
	}
	wantGrants := []string{"codex.global", "pi.global"}
	if !reflect.DeepEqual(state.Skills[0].Agents, wantGrants) {
		t.Fatalf("skill agents = %#v, want %#v (portable grants excluded)", state.Skills[0].Agents, wantGrants)
	}
	if err := setGrant(repo, "alpha", grantRequest{Consumer: "pi.portable", Enabled: true}); err == nil {
		t.Fatal("setGrant accepted a portable consumer")
	}
}

func TestDashboardAgentRankIsDeterministic(t *testing.T) {
	ids := []string{"zeta.global", "pi.global", "codex.global", "alpha.global", "claude.global"}
	sort.Slice(ids, func(i, j int) bool {
		ri, rj := dashboardAgentRank(ids[i]), dashboardAgentRank(ids[j])
		if ri != rj {
			return ri < rj
		}
		return ids[i] < ids[j]
	})
	want := []string{"codex.global", "claude.global", "pi.global", "alpha.global", "zeta.global"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ranked = %#v, want %#v", ids, want)
	}
}

func TestProducerPublishIsAtomicForOwnedSkillSet(t *testing.T) {
	repo := newTestRepository(t)
	producerRoot := t.TempDir()
	writeNamedSkill(t, filepath.Join(producerRoot, "dist", "one"), "one", "new one")
	writeNamedSkill(t, filepath.Join(producerRoot, "dist", "two"), "two", "new two")
	producer := struct {
		Root    string           `json:"root"`
		Build   ProducerBuild    `json:"build"`
		Outputs []ProducerOutput `json:"outputs"`
		Skills  []string         `json:"skills"`
	}{producerRoot, ProducerBuild{Argv: []string{"true"}}, []ProducerOutput{{Path: "dist"}}, []string{"one", "two"}}
	data, _ := json.MarshalIndent(producer, "", "  ")
	writeFile(t, filepath.Join(repo, "producers", "example.json"), string(data))
	if err := os.MkdirAll(filepath.Join(repo, "skills", "retired", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishProducers(repo, []string{"example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "skills", "retired")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publish preserved an empty retired skill tree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "skills", "one", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "skills", "two", "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(producerRoot, "dist", "two", ".env.local"), "secret")
	writeNamedSkill(t, filepath.Join(producerRoot, "dist", "one"), "one", "changed one")
	if _, err := PublishProducers(repo, []string{"example"}); err == nil {
		t.Fatal("invalid producer publish succeeded")
	}
	metadata, err := readSkillMetadata(filepath.Join(repo, "skills", "one"))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Description != "new one" {
		t.Fatalf("partial update escaped transaction: %q", metadata.Description)
	}
}

func TestUpdateReportCarriesArtifactAndCatalogHandoff(t *testing.T) {
	repo := newTestRepository(t)
	producerRoot := t.TempDir()
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "old")
	writeNamedSkill(t, filepath.Join(producerRoot, "dist", "alpha"), "alpha", "new")
	producer := struct {
		Root    string           `json:"root"`
		Build   ProducerBuild    `json:"build"`
		Outputs []ProducerOutput `json:"outputs"`
		Skills  []string         `json:"skills"`
	}{producerRoot, ProducerBuild{Argv: []string{"true"}}, []ProducerOutput{{Path: "dist"}}, []string{"alpha"}}
	data, _ := json.MarshalIndent(producer, "", "  ")
	writeFile(t, filepath.Join(repo, "producers", "example.json"), string(data))
	head := commitAll(t, repo, "old catalog")

	report, err := UpdateProducers(repo, []string{"example"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	artifact := report.Producers[0].Artifacts[0]
	if artifact.PreviousTreeHash == "" || artifact.TreeHash == "" || artifact.PreviousTreeHash == artifact.TreeHash {
		t.Fatalf("artifact hashes = old %q new %q", artifact.PreviousTreeHash, artifact.TreeHash)
	}
	if report.Handoff.Head != head || !report.Handoff.PendingCommit || !report.Handoff.BuildReadsCommittedCatalogOnly {
		t.Fatalf("handoff = %#v", report.Handoff)
	}
	if len(report.Handoff.ChangedFiles) != 1 || report.Handoff.ChangedFiles[0] != "skills/alpha/SKILL.md" {
		t.Fatalf("changed files = %#v", report.Handoff.ChangedFiles)
	}
	if len(report.Handoff.NextCommand) == 0 {
		t.Fatal("handoff omitted next command")
	}
	if len(report.Sources) != 1 || report.Sources[0].Git {
		t.Fatalf("non-Git Producer source = %#v", report.Sources)
	}
}

func TestAddProducerPersistsTrimmedNote(t *testing.T) {
	repo := newTestRepository(t)
	if _, err := runGit(repo, "config", "user.name", "sm-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repo, "config", "user.email", "sm-test@example.com"); err != nil {
		t.Fatal(err)
	}
	producerRoot := t.TempDir()
	writeNamedSkill(t, filepath.Join(producerRoot, "dist", "alpha"), "alpha", "Alpha skill")
	if err := addProducer(repo, producerRequest{ID: "example", Root: producerRoot, Note: "  中文备注  ", Build: "make skill", Output: "dist"}); err != nil {
		t.Fatal(err)
	}
	producers, err := loadProducers(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(producers) != 1 || producers[0].Note != "中文备注" {
		t.Fatalf("producers = %#v", producers)
	}
}

func TestDashboardStateMarksUnavailableProducerAsError(t *testing.T) {
	repo := newTestRepository(t)
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "Alpha skill")
	producer := Producer{
		ID: "example", Root: filepath.Join(t.TempDir(), "missing"), Build: ProducerBuild{Argv: []string{"true"}},
		Outputs: []ProducerOutput{{Path: "dist"}}, Skills: []string{"alpha"},
	}
	if err := writeProducer(repo, producer); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "initial")

	state, err := dashboardState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Producers) != 1 || state.Producers[0].Status != "error" || state.Producers[0].StatusLabel != "来源不可用" {
		t.Fatalf("producer state = %#v", state.Producers)
	}
	if len(state.Skills) != 1 || state.Skills[0].Update != "error" {
		t.Fatalf("skill state = %#v", state.Skills)
	}
}

func TestDashboardStateNeverShowsCurrentWhenProducerScanFails(t *testing.T) {
	repo := newTestRepository(t)
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "Alpha skill")
	for _, id := range []string{"one", "two"} {
		root := t.TempDir()
		writeNamedSkill(t, filepath.Join(root, "dist", "alpha"), "alpha", "Alpha skill")
		producer := Producer{
			ID: id, Root: root, Build: ProducerBuild{Argv: []string{"true"}},
			Outputs: []ProducerOutput{{Path: "dist"}}, Skills: []string{"alpha"},
		}
		if err := writeProducer(repo, producer); err != nil {
			t.Fatal(err)
		}
	}
	commitAll(t, repo, "initial")

	state, err := dashboardState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Producers) != 2 {
		t.Fatalf("producer state = %#v", state.Producers)
	}
	for _, producer := range state.Producers {
		if producer.Status != "error" || producer.StatusLabel != "扫描失败" {
			t.Fatalf("producer state = %#v", state.Producers)
		}
	}
	if len(state.Skills) != 1 || state.Skills[0].Update != "error" {
		t.Fatalf("skill state = %#v", state.Skills)
	}
}

func writeNamedSkill(t *testing.T, root, id, description string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: "+id+"\ndescription: "+description+"\n---\n")
}
