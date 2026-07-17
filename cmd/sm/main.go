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
	if err := skillmanager.RunCLI(os.Args[1:], os.Stdout, os.Stderr); err != nil {
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
