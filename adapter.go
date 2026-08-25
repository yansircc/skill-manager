package skillmanager

import (
	"fmt"
	"os/exec"
)

// ProofKind identifies how an adapter proved its discovery or launch closure.
type ProofKind string

const (
	// ProofKindAgentAPI proves closure through the Agent's discovery API.
	ProofKindAgentAPI ProofKind = "agent-api"
	// ProofKindLaunchClosure proves closure from constructed launch arguments.
	ProofKindLaunchClosure ProofKind = "launch-closure"
	// ProofKindFilesystemGuard proves closure by inspecting isolated profile and project paths.
	ProofKindFilesystemGuard ProofKind = "filesystem-guard"
)

// DiscoveryRecord is the normalized Agent skill discovery observation.
type DiscoveryRecord struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Scope   string `json:"scope,omitempty"`
	Source  string `json:"source,omitempty"`
	Enabled bool   `json:"enabled"`
}

// VerificationEvidence is adapter proof attached to a Verification result.
type VerificationEvidence struct {
	Kind      ProofKind         `json:"kind"`
	Discovery []DiscoveryRecord `json:"discovery,omitempty"`
	Command   []string          `json:"command,omitempty"`
}

// AdapterVerifyResult is the adapter-owned portion of verification.
type AdapterVerifyResult struct {
	ExternalSkills []string
	Evidence       *VerificationEvidence
}

// AgentAdapter centralizes projection preparation, launch, and verification for one Agent.
type AgentAdapter interface {
	Name() string
	Persistent() bool
	PrepareProjection(root, consumerName string, consumer Consumer) error
	LaunchCommand(built Result, agentArgs []string) (Result, *exec.Cmd, error)
	Verify(built Result, closed bool) (AdapterVerifyResult, error)
}

var agentAdapters = map[string]AgentAdapter{
	"directory": directoryAdapter{},
	"codex":     codexAdapter{},
	"claude":    claudeAdapter{},
	"pi":        piAdapter{},
}

func lookupAgentAdapter(name string) (AgentAdapter, error) {
	adapter, ok := agentAdapters[name]
	if !ok {
		return nil, fmt.Errorf("unsupported adapter %q", name)
	}
	return adapter, nil
}

type directoryAdapter struct{}

func (directoryAdapter) Name() string     { return "directory" }
func (directoryAdapter) Persistent() bool { return true }
func (directoryAdapter) PrepareProjection(string, string, Consumer) error {
	return nil
}
func (directoryAdapter) LaunchCommand(Result, []string) (Result, *exec.Cmd, error) {
	return Result{}, nil, fmt.Errorf("directory adapter has no exec contract")
}
func (directoryAdapter) Verify(Result, bool) (AdapterVerifyResult, error) {
	return AdapterVerifyResult{}, nil
}
