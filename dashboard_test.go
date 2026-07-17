package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		Build   ProducerBuild    `json:"build"`
		Outputs []ProducerOutput `json:"outputs"`
		Skills  []string         `json:"skills"`
	}{producerRoot, ProducerBuild{Argv: []string{"make", "skill"}}, []ProducerOutput{{Path: "dist"}}, []string{"alpha"}}
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
	if _, err := dashboardState(repo); err == nil {
		t.Fatal("dashboard accepted an empty skill directory")
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
	if _, err := PublishProducers(repo, []string{"example"}); err != nil {
		t.Fatal(err)
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

func writeNamedSkill(t *testing.T, root, id, description string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: "+id+"\ndescription: "+description+"\n---\n")
}
