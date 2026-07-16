package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

var version = "dev"

func main() {
	if err := runCLI(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		var exitError *ProcessExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.Code)
		}
		fmt.Fprintln(os.Stderr, "sm:", err)
		os.Exit(1)
	}
}

func runCLI(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return flag.ErrHelp
	}

	switch args[0] {
	case "scan":
		fs := newFlagSet("scan", stderr)
		repo := fs.String("repo", ".", "SSOT repository")
		asJSON := fs.Bool("json", false, "emit JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			return fmt.Errorf("scan requires at least one discovery root")
		}
		candidates, err := Scan(*repo, fs.Args())
		if err != nil {
			return err
		}
		if *asJSON {
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(candidates)
		}
		for _, candidate := range candidates {
			fmt.Fprintf(stdout, "%s\t%s\n", candidate.ID, candidate.Path)
		}
		return nil

	case "init":
		fs := newFlagSet("init", stderr)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		path := "."
		if fs.NArg() > 1 {
			return fmt.Errorf("usage: sm init [path]")
		}
		if fs.NArg() == 1 {
			path = fs.Arg(0)
		}
		root, err := Init(path)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, root)
		return nil

	case "adopt":
		fs := newFlagSet("adopt", stderr)
		repo := fs.String("repo", ".", "SSOT repository")
		id := fs.String("id", "", "stable skill id; defaults to source directory name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: sm adopt [--repo path] [--id id] source")
		}
		destination, err := Adopt(*repo, fs.Arg(0), *id)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, destination)
		return nil

	case "build", "apply", "verify", "exec":
		fs := newFlagSet(args[0], stderr)
		repo := fs.String("repo", ".", "SSOT repository")
		ref := fs.String("ref", "HEAD", "published Git commit")
		cache := fs.String("cache", "", "generation cache; defaults to ~/.cache/sm")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if args[0] != "exec" && fs.NArg() != 1 || args[0] == "exec" && fs.NArg() < 1 {
			return fmt.Errorf("usage: sm %s [--repo path] [--ref commit] consumer", args[0])
		}
		consumer := fs.Arg(0)
		switch args[0] {
		case "build":
			result, err := Build(*repo, *ref, consumer, *cache)
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, result.Generation)
			return nil
		case "apply":
			result, err := Apply(*repo, *ref, consumer, *cache)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s -> %s\n", result.Target, result.Generation)
			return nil
		case "exec":
			agentArgs := fs.Args()[1:]
			if len(agentArgs) > 0 && agentArgs[0] == "--" {
				agentArgs = agentArgs[1:]
			}
			if err := RunConsumer(*repo, *ref, consumer, *cache, agentArgs, os.Stdin, stdout, stderr); err != nil {
				return err
			}
			return nil
		default:
			result, err := Verify(*repo, *ref, consumer, *cache)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "verified %s at %s\n", result.Consumer, result.Commit)
			return nil
		}

	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `sm compiles an immutable Agent skill projection from a Git SSOT.

Usage:
  sm scan [--repo path] [--json] root...
  sm init [path]
  sm adopt [--repo path] [--id id] source
  sm build [--repo path] [--ref commit] consumer
  sm apply [--repo path] [--ref commit] consumer
  sm verify [--repo path] [--ref commit] consumer
  sm exec [--repo path] [--ref commit] consumer [-- agent arguments...]
  sm version`)
}
