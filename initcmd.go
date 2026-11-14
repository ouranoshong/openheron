package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/initcfg"
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
	noTools := fs.Bool("no-tools", false, "leave the tool configurations under tools/ unwritten (they are written by default)")
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
	// NewServiceAt, not NewService: init writes into the directory it was
	// pointed at and never reads $OPENHERON_CONFIG_DIR. The environment
	// variable decides which configuration the server reads, and honoring it
	// here would write the new configuration somewhere else than asked.
	svc, err := spec.NewServiceAt(root, abs)
	if err != nil {
		return err
	}
	// ★ The keyring is a global OS resource, so it is asked for by name —
	//   here, where the command may read a key to say whether one is
	//   there. Nothing else reaches it.
	svc.SetSecretStore(spec.KeyringStore())

	plan, err := initcfg.BuildPlan(svc, initcfg.Options{
		Model:    *model,
		Provider: *provider,
		BaseURL:  *baseURL,
		NoTools:  *noTools,
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
		if plan.SecretHint != "" {
			out["secret"] = plan.SecretHint
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
		// ★ Both places are named, and the order is the order of
		//   precedence: the environment wins over the keyring. A user who
		//   stores a key while an old export is still in the shell would
		//   otherwise see "stored" and a key that is not in force.
		fmt.Fprintf(stdout, "the api key is read at request time from %s, or from the keyring entry %s\n",
			plan.EnvHint, plan.SecretHint)
		fmt.Fprintf(stdout, "store one with: openheron secret set llm <provider>\n")
	}
	return nil
}

func printInitUsage(stdout io.Writer) {
	fmt.Fprintln(stdout, "usage: openheron init [--dir <path>] [--model <provider>/<model>] [--provider <id>]")
	fmt.Fprintln(stdout, "                [--base-url <url>] [--no-tools]")
	fmt.Fprintln(stdout, "                [--force] [--dry-run] [--json]")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Creates a runnable project-level configuration in <dir>/.openheron.")
	fmt.Fprintln(stdout, "Only the files that are actually missing are written; files that already")
	fmt.Fprintln(stdout, "exist at the user level are inherited, never replaced.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Written: agents/main.md, agents/task.md, agents/agent-expert.md,")
	fmt.Fprintln(stdout, "        agents/skill-expert.md, llm/<provider>.yaml, server/config.yaml,")
	fmt.Fprintln(stdout, "        tools/{filesystem,http,kb,web_search,wikipedia}.yaml,")
	fmt.Fprintln(stdout, "        mcps/{fetch,time,deepwiki}.yaml,")
	fmt.Fprintln(stdout, "        skills/openheron-config-{map,edit,templates,validate}/SKILL.md.")
	fmt.Fprintln(stdout, "--no-tools leaves tools/ unwritten; every other partition is always written.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "The api key is not written to the configuration: the provider's name says")
	fmt.Fprintln(stdout, "where it comes from — LLM_<PROVIDER>_API_KEY, or the keyring entry")
	fmt.Fprintln(stdout, "llm:<provider>. Store one with: openheron secret set llm <provider>")
}
