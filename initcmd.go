package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"openheron/agentkit/spec"
	"openheron/initcfg"
)

func initCmd(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	dir := fs.String("dir", "", "target directory (default: the current directory)")
	configDir := fs.String("config-dir", "",
		"the config root to write into (default: <dir>/.openheron)")
	model := fs.String("model", "", "model to use, as <provider>/<model>")
	provider := fs.String("provider", "", "provider template to use (default: deepseek)")
	baseURL := fs.String("base-url", "", "endpoint for a new provider file")
	env := fs.String("env", "", "environment variable holding the api key")
	noPolicy := fs.Bool("no-policy", false, "do not write policy/permissions.yaml")
	force := fs.Bool("force", false, "overwrite files that already exist")
	dryRun := fs.Bool("dry-run", false, "report what would be written, write nothing")
	asJSON := fs.Bool("json", false, "machine-readable output")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printInitUsage(stdout)
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q; openheron init takes flags only", fs.Arg(0))
	}

	target := *dir
	if target == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("openheron init: resolve working directory: %w", err)
		}
		target = wd
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("openheron init: resolve target directory %q: %w", target, err)
	}

	root := *configDir
	if root == "" {
		root = filepath.Join(abs, spec.DefaultBaseDirName)
	}
	svc, err := spec.NewServiceAt(root, abs)
	if err != nil {
		return err
	}

	plan, err := initcfg.BuildPlan(svc, initcfg.Options{
		Model:    *model,
		Provider: *provider,
		BaseURL:  *baseURL,
		Env:      *env,
		NoPolicy: *noPolicy,
		Force:    *force,
	})
	if err != nil {
		return err
	}
	if !*dryRun {
		if err := plan.Apply(svc); err != nil {
			return err
		}
	}
	return reportInit(plan, abs, *dryRun, *asJSON, stdout)
}

func reportInit(plan *initcfg.Plan, root string, dryRun, asJSON bool, stdout io.Writer) error {
	created := make([]string, 0, len(plan.Files))
	for _, f := range plan.Files {
		created = append(created, filepath.Join(spec.DefaultBaseDirName, f.Rel))
	}

	if asJSON {
		out := map[string]any{
			"dir":     plan.Dir,
			"model":   plan.Model,
			"created": created,
			"skipped": plan.Skipped,
			"notes":   plan.Notes,
			"dryRun":  dryRun,
		}
		if plan.EnvHint != "" {
			out["env"] = plan.EnvHint
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, string(b))
		return nil
	}

	for _, n := range plan.Notes {
		fmt.Fprintf(stdout, "note: %s\n", n)
	}
	for _, s := range plan.Skipped {
		fmt.Fprintf(stdout, "kept    %s (already exists)\n", filepath.Join(spec.DefaultBaseDirName, s))
	}
	for _, c := range created {
		fmt.Fprintf(stdout, "created %s\n", c)
	}

	if len(created) == 0 {
		fmt.Fprintf(stdout, "\nnothing to do: %s is already covered by the existing configuration\n", root)
		return nil
	}
	if dryRun {
		fmt.Fprintf(stdout, "\ndry run: nothing was written (would write %d file(s) to %s)\n", len(created), plan.Dir)
		return nil
	}
	fmt.Fprintf(stdout, "\nconfig written to %s\n", plan.Dir)
	if plan.EnvHint != "" {
		fmt.Fprintf(stdout, "the api key is read from the environment at request time: export %s=...\n", plan.EnvHint)
	}
	return nil
}

func printInitUsage(stdout io.Writer) {
	fmt.Fprintln(stdout, "usage: openheron init [--dir <path>] [--model <provider>/<model>] [--provider <id>]")
	fmt.Fprintln(stdout, "                [--base-url <url>] [--env <NAME>] [--no-policy]")
	fmt.Fprintln(stdout, "                [--force] [--dry-run] [--json]")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Creates a minimal, runnable project-level configuration in <dir>/.openheron.")
	fmt.Fprintln(stdout, "Only the files that are actually missing are written; files that already")
	fmt.Fprintln(stdout, "exist at the user level are inherited, never replaced.")
}
