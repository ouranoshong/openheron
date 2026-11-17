package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/initcfg"
)

func initCmd(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	configRoot := fs.String("config-root", "",
		"the config root to write into (default: <openheron-root>/configs)")
	model := fs.String("model", "", "model to use, as <provider>/<model>")
	provider := fs.String("provider", "", "provider template to use (default: deepseek)")
	baseURL := fs.String("base-url", "", "endpoint for a new provider file")
	noTools := fs.Bool("no-tools", false, "leave the tool configurations under tools/ unwritten (they are written by default)")
	force := fs.Bool("force", false, "overwrite files that already exist")
	dryRun := fs.Bool("dry-run", false, "report what would be written, write nothing")
	asJSON := fs.Bool("json", false, "machine-readable output")

	// ★ --dir is retired, and it is not an alias for --config-root: it named
	//   a **project** directory, and init writes a **config root**. ★ The two
	//   are different directories — accepting the old name for the new one
	//   would write the configuration somewhere the caller did not ask for.
	for _, a := range args {
		if a == "--dir" || strings.HasPrefix(a, "--dir=") {
			return fmt.Errorf("--dir was replaced by --config-root: it named a project directory, " +
				"and init writes the config root itself")
		}
	}
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

	home, err := resolveOpenheronRoot("")
	if err != nil {
		return err
	}

	// ★ The default project root: the directory a session that belongs to no
	//   project works in. ★ The server creates it when it starts, but "init"
	//   is where the base directory is laid out — and a person who looks
	//   into it afterwards must find the whole layout, not half of it.
	//
	// ★ A dry run writes nothing, so the directory is only created when the
	//   run is real.
	defaultProject := ""
	if !*dryRun {
		defaultProject, err = spec.EnsureDefaultProjectRoot(home)
		if err != nil {
			return fmt.Errorf("openheron init: %w", err)
		}
	}
	root := strings.TrimSpace(*configRoot)
	if root == "" {
		// ★ The default is the config root the server reads, so that "init
		//   and then start" is the whole story. ★ It is not <project>/.openheron
		//   any more: the configuration lives in the user-level base
		//   directory, and the project directory is a project's business.
		root = filepath.Join(home, spec.DefaultConfigDirName)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("openheron init: resolve config root %q: %w", root, err)
	}
	projectRoot, err := commandProjectRoot()
	if err != nil {
		return err
	}

	// NewServiceAt, not NewService: init writes into the directory it was
	// pointed at and never reads $OPENHERON_CONFIG_ROOT. The environment
	// variable decides which configuration the server reads, and honoring it
	// here would write the new configuration somewhere else than asked.
	svc, err := spec.NewServiceAt(abs, projectRoot, spec.WithHome(home))
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
	return reportInit(plan, abs, defaultProject, *dryRun, *asJSON, stdout)
}

func reportInit(plan *initcfg.Plan, root, defaultProject string, dryRun, asJSON bool, stdout io.Writer) error {
	created := make([]string, 0, len(plan.Files))
	for _, f := range plan.Files {
		// ★ The paths are relative to the **config root** — the tree's own
		//   layout, not the project's. The config root may live far away
		//   from the working directory, and a path that only means something
		//   relative to it is the honest one.
		created = append(created, f.Rel)
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
		if defaultProject != "" {
			out["project_root"] = defaultProject
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
		fmt.Fprintf(stdout, "kept    %s (already exists)\n", s)
	}
	for _, c := range created {
		fmt.Fprintf(stdout, "created %s\n", c)
	}
	// ★ The default project root is a directory, not a file: it is where a
	//   session that belongs to no project does its work, and the command
	//   would not start without it.
	if defaultProject != "" {
		fmt.Fprintf(stdout, "created %s (the default project root)\n", defaultProject)
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
		fmt.Fprintf(stdout, "the api key is read from %s when the configuration loads, or from the keyring entry %s\n",
			plan.EnvHint, plan.SecretHint)
		fmt.Fprintf(stdout, "store one with: openheron secret set llm <provider>\n")
	}
	return nil
}

func printInitUsage(stdout io.Writer) {
	fmt.Fprintln(stdout, "usage: openheron init [--config-root <path>] [--model <provider>/<model>] [--provider <id>]")
	fmt.Fprintln(stdout, "                [--base-url <url>] [--no-tools]")
	fmt.Fprintln(stdout, "                [--force] [--dry-run] [--json]")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Creates a runnable configuration in the config root: <openheron-root>/configs")
	fmt.Fprintln(stdout, "by default, which is the one the server reads. Only the files that are")
	fmt.Fprintln(stdout, "actually missing are written; files that already exist are inherited,")
	fmt.Fprintln(stdout, "never replaced.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Written: agents/main.md, agents/task.md, agents/agent-expert.md,")
	fmt.Fprintln(stdout, "        agents/skill-expert.md, llm/<provider>.yaml, server/config.yaml,")
	fmt.Fprintln(stdout, "        tools/{filesystem,http,kb,web_search,wikipedia,config}.yaml,")
	fmt.Fprintln(stdout, "        mcps/{fetch,time,deepwiki}.yaml,")
	fmt.Fprintln(stdout, "        skills/openheron-config-{map,edit,templates,validate}/SKILL.md,")
	fmt.Fprintln(stdout, "        skills/openheron-agent-authoring/SKILL.md.")
	fmt.Fprintln(stdout, "--no-tools leaves tools/ unwritten; every other partition is always written.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "The api key is not written to the configuration: the provider's name says")
	fmt.Fprintln(stdout, "where it comes from — LLM_<PROVIDER>_API_KEY, or the keyring entry")
	fmt.Fprintln(stdout, "llm:<provider>. Store one with: openheron secret set llm <provider>")
}
