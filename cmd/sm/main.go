package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	skillmanager "github.com/yansircc/skill-manager"
)

var version = "dev"

func main() {
	skillmanager.Version = version
	executable, executableErr := os.Executable()
	var err error
	if executableErr != nil {
		err = executableErr
	} else {
		managed, detectErr := skillmanager.DetectManagedExecutableInvocation(executable)
		if detectErr != nil {
			err = detectErr
		} else if managed {
			err = skillmanager.RunManagedExecutable(executable, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
		} else {
			err = skillmanager.RunCLI(os.Args[1:], os.Stdout, os.Stderr)
		}
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		var exitError *skillmanager.ProcessExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.Code)
		}
		fmt.Fprintln(os.Stderr, "sm:", err)
		os.Exit(1)
	}
}
