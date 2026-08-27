package skillmanager

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// StatusReport describes the relationship between producer sources, published
// artifacts, and the committed SSOT catalog.
type StatusReport struct {
	Producers []StatusProducer `json:"producers"`
	Catalog   CatalogHandoff   `json:"catalog"`
}

type StatusProducer struct {
	ProducerID string           `json:"producerId"`
	Root       string           `json:"root"`
	Source     ProducerSource   `json:"source"`
	Artifacts  []StatusArtifact `json:"artifacts"`
}

type StatusArtifact struct {
	SkillID        string `json:"skillId"`
	ArtifactHash   string `json:"artifactHash,omitempty"`
	CatalogPresent bool   `json:"catalogPresent"`
}

func Status(repo string, ids []string) (StatusReport, error) {
	root, err := repositoryRoot(repo)
	if err != nil {
		return StatusReport{}, err
	}
	all, err := loadProducers(root)
	if err != nil {
		return StatusReport{}, err
	}
	selected, err := selectProducers(all, ids)
	if err != nil {
		return StatusReport{}, err
	}
	catalog, err := catalogHandoff(root)
	if err != nil {
		return StatusReport{}, err
	}
	result := StatusReport{Catalog: catalog}
	for _, producer := range selected {
		item := StatusProducer{ProducerID: producer.ID, Root: producer.Root, Source: observeProducerSource(producer)}
		for _, skillID := range producer.Skills {
			artifact := StatusArtifact{SkillID: skillID}
			if metadata, err := readSkillMetadata(filepath.Join(root, "skills", skillID)); err == nil {
				artifact.CatalogPresent = metadata.Name != ""
			}
			item.Artifacts = append(item.Artifacts, artifact)
		}
		result.Producers = append(result.Producers, item)
	}
	return result, nil
}

func PrintStatus(report StatusReport, stdout io.Writer) {
	for _, producer := range report.Producers {
		fmt.Fprintf(stdout, "producer\t%s\troot=%s\thead=%s\tdirty=%t\n", producer.ProducerID, producer.Root, producer.Source.ObservedHead, producer.Source.Dirty)
		for _, artifact := range producer.Artifacts {
			fmt.Fprintf(stdout, "skill\t%s\tcatalogPresent=%t\n", artifact.SkillID, artifact.CatalogPresent)
		}
	}
	fmt.Fprintf(stdout, "catalog\thead=%s\tpendingCommit=%t\n", report.Catalog.Head, report.Catalog.PendingCommit)
	if report.Catalog.PendingCommit {
		fmt.Fprintf(stdout, "next\t%s\n", formatArgv(report.Catalog.NextCommand))
	}
}

func gitStatusSummary(root string) string {
	output, err := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=normal").Output()
	if err != nil {
		return "git status unavailable"
	}
	return strings.TrimSpace(string(output))
}
