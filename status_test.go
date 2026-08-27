package skillmanager

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusReportsProducerAndCatalogState(t *testing.T) {
	repo := newTestRepository(t)
	configureTestGitIdentity(t, repo)
	source := t.TempDir()
	if _, err := runGit(source, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(source, "config", "user.name", "sm-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(source, "config", "user.email", "sm-test@example.com"); err != nil {
		t.Fatal(err)
	}
	writeNamedSkill(t, filepath.Join(source, "dist", "alpha"), "alpha", "built alpha")
	if _, err := runGit(source, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(source, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	if err := writeProducer(repo, Producer{
		ID: "example", Root: source, Build: ProducerBuild{Argv: []string{"true"}},
		Outputs: []ProducerOutput{{Path: "dist"}}, Skills: []string{"alpha"},
	}); err != nil {
		t.Fatal(err)
	}
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "catalog alpha")
	commitAll(t, repo, "initial")

	report, err := Status(repo, []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Producers) != 1 || report.Producers[0].Source.Dirty {
		t.Fatalf("unexpected producer state: %#v", report.Producers)
	}
	if !report.Producers[0].Artifacts[0].CatalogPresent {
		t.Fatal("catalog skill not reported")
	}
	if report.Catalog.PendingCommit {
		t.Fatal("clean catalog reported pending")
	}

	var output bytes.Buffer
	PrintStatus(report, &output)
	text := output.String()
	for _, expected := range []string{"producer\texample", "head=", "dirty=false", "skill\talpha\tcatalogPresent=true", "pendingCommit=false"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("status output missing %q: %s", expected, text)
		}
	}
}

func TestStatusReportsDirtyProducerAndPendingCatalog(t *testing.T) {
	repo := newTestRepository(t)
	configureTestGitIdentity(t, repo)
	source := t.TempDir()
	if _, err := runGit(source, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(source, "config", "user.name", "sm-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(source, "config", "user.email", "sm-test@example.com"); err != nil {
		t.Fatal(err)
	}
	writeNamedSkill(t, filepath.Join(source, "dist", "alpha"), "alpha", "built alpha")
	if _, err := runGit(source, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(source, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	if err := writeProducer(repo, Producer{ID: "example", Root: source, Build: ProducerBuild{Argv: []string{"true"}}, Outputs: []ProducerOutput{{Path: "dist"}}, Skills: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "catalog alpha")
	commitAll(t, repo, "initial")
	writeFile(t, filepath.Join(source, "dirty.txt"), "dirty source")
	writeNamedSkill(t, filepath.Join(repo, "skills", "alpha"), "alpha", "pending alpha")

	report, err := Status(repo, []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Producers[0].Source.Dirty {
		t.Fatal("dirty producer not reported")
	}
	if !report.Catalog.PendingCommit {
		t.Fatal("pending catalog not reported")
	}
	if len(report.Catalog.NextCommand) == 0 {
		t.Fatal("missing catalog next command")
	}
}
