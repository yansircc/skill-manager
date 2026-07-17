package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

var version = "dev"

func currentVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

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
	case "dashboard", "open":
		fs := newFlagSet(args[0], stderr)
		repo := fs.String("repo", "~/.sm", "SSOT repository")
		listen := fs.String("listen", "127.0.0.1:7777", "dashboard listen address")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("usage: sm %s [--repo path] [--listen address]", args[0])
		}
		root, err := expandHome(*repo)
		if err != nil {
			return err
		}
		if args[0] == "open" {
			return OpenDashboard(root, *listen)
		}
		return RunDashboard(root, *listen)
	case "producers":
		fs := newFlagSet("producers", stderr)
		repo := fs.String("repo", ".", "SSOT repository")
		asJSON := fs.Bool("json", false, "emit JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("usage: sm producers [--repo path] [--json]")
		}
		producers, err := loadProducers(*repo)
		if err != nil {
			return err
		}
		if *asJSON {
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(producers)
		}
		for _, producer := range producers {
			fmt.Fprintf(stdout, "%s\t%s\t%d skills\n", producer.ID, producer.Root, len(producer.Skills))
		}
		return nil
	case "scan":
		fs := newFlagSet("scan", stderr)
		repo := fs.String("repo", ".", "SSOT repository")
		asJSON := fs.Bool("json", false, "emit JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		report, err := ScanProducers(*repo, fs.Args())
		if err != nil {
			return err
		}
		if *asJSON {
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(report)
		}
		for _, producer := range report.Producers {
			if producer.Error != "" {
				fmt.Fprintf(stdout, "%s\terror\t%s\n", producer.Producer.ID, producer.Error)
				continue
			}
			for _, artifact := range producer.Artifacts {
				fmt.Fprintf(stdout, "%s\t%s\t%s\n", producer.Producer.ID, artifact.SkillID, artifact.State)
			}
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

	case "produce", "publish", "update":
		fs := newFlagSet(args[0], stderr)
		repo := fs.String("repo", ".", "SSOT repository")
		asJSON := fs.Bool("json", false, "emit JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			return fmt.Errorf("%s requires at least one producer", args[0])
		}
		if args[0] == "produce" {
			return Produce(*repo, fs.Args(), stdout, stderr)
		}
		var report PublishReport
		var err error
		if args[0] == "publish" {
			report, err = PublishProducers(*repo, fs.Args())
		} else {
			report, err = UpdateProducers(*repo, fs.Args(), stdout, stderr)
		}
		if err != nil {
			return err
		}
		if *asJSON {
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(report)
		}
		for _, producer := range report.Producers {
			fmt.Fprintf(stdout, "%s\tpublished\n", producer.Producer.ID)
		}
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
		fmt.Fprintln(stdout, currentVersion())
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
  sm open [--repo path] [--listen address]
  sm dashboard [--repo path] [--listen address]
  sm producers [--repo path] [--json]
  sm scan [--repo path] [--json] [producer...]
  sm produce [--repo path] producer...
  sm publish [--repo path] [--json] producer...
  sm update [--repo path] [--json] producer...
  sm init [path]
  sm build [--repo path] [--ref commit] consumer
  sm apply [--repo path] [--ref commit] consumer
  sm verify [--repo path] [--ref commit] consumer
  sm exec [--repo path] [--ref commit] consumer [-- agent arguments...]
  sm version`)
}
