package skillmanager

import (
	"fmt"
	"os/exec"
	"strings"
)

type piAdapter struct{}

func (piAdapter) Name() string     { return "pi" }
func (piAdapter) Persistent() bool { return false }

func (piAdapter) PrepareProjection(string, string, Consumer) error {
	return nil
}

func (piAdapter) LaunchCommand(built Result, agentArgs []string) (Result, *exec.Cmd, error) {
	return piAgentCommand(built, agentArgs)
}

func (piAdapter) Verify(built Result, closed bool) (AdapterVerifyResult, error) {
	if !closed {
		return AdapterVerifyResult{}, fmt.Errorf("consumer %q uses ephemeral pi activation; use sm exec or verify --closed", built.Consumer)
	}
	arguments := piLaunchArguments(built.Generation)
	if err := validatePiLaunchClosure(arguments, built.Generation); err != nil {
		return AdapterVerifyResult{}, err
	}
	return AdapterVerifyResult{
		Evidence: &VerificationEvidence{
			Kind:    ProofKindLaunchClosure,
			Command: append([]string(nil), arguments...),
		},
	}, nil
}

func piAgentCommand(built Result, agentArgs []string) (Result, *exec.Cmd, error) {
	binary, err := findExecutable("pi")
	if err != nil {
		return Result{}, nil, fmt.Errorf("find pi executable: %w", err)
	}
	arguments := append(piLaunchArguments(built.Generation), agentArgs...)
	return built, exec.Command(binary, arguments...), nil
}

func piLaunchArguments(generation string) []string {
	return []string{"--no-extensions", "--no-skills", "--skill", generation}
}

func validatePiLaunchClosure(arguments []string, generation string) error {
	if len(arguments) < 4 {
		return fmt.Errorf("pi launch closure missing discovery-disabling arguments")
	}
	if arguments[0] != "--no-extensions" || arguments[1] != "--no-skills" {
		return fmt.Errorf("pi launch closure must disable default discovery with --no-extensions and --no-skills")
	}
	if arguments[2] != "--skill" || arguments[3] != generation {
		return fmt.Errorf("pi launch closure must select the explicit generation with --skill")
	}
	for index := 4; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--skill" || strings.HasPrefix(argument, "--skill=") ||
			argument == "--extension" || strings.HasPrefix(argument, "--extension=") || argument == "-e" {
			return fmt.Errorf("pi launch closure must not reintroduce default discovery through %q", argument)
		}
	}
	return nil
}
