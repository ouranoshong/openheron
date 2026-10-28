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

// initCmd 是 `openheron init …` 子命令。
//
// ★ 它在**当前目录**建一份最小可运行配置，而且只补确实缺的东西 ——
//
//	已存在的文件一律不动（同名文件是整体替换语义，写下去等于把用户
//	自己那份悄悄换掉）。见 docs/init-design.md。
//
// ★ 落点是**生效的那个配置根**：给了 --config-dir 就写那里，
//
//	否则写 `<目标目录>/.openheron`。
//
// ★ 它只需要 config —— 不需要 session manager、checkpoint store、hertz、
//
//	agent builder。启动路径比 run() 短得多。
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

	// ★ 落点是**指定的那个目录**，不上溯探测 —— 祖先目录恰好有 .openheron/
	//	时不该写到那里去（那正是运行期的规则，不是写配置的规则）。
	//
	//	--config-dir 给的是**配置根本身**（里面直接有 llm/ agents/），
	//	与运行期的 OPENHERON_CONFIG_DIR 同一个值语义。
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

// reportInit 打印结果。
//
// ★ 运行时输出一律英文（AGENTS.md 的语言约定）。
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

// printInitUsage 打印用法。
func printInitUsage(stdout io.Writer) {
	fmt.Fprintln(stdout, "usage: openheron init [--dir <path>] [--model <provider>/<model>] [--provider <id>]")
	fmt.Fprintln(stdout, "                [--base-url <url>] [--env <NAME>] [--no-policy]")
	fmt.Fprintln(stdout, "                [--force] [--dry-run] [--json]")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Creates a minimal, runnable project-level configuration in <dir>/.openheron.")
	fmt.Fprintln(stdout, "Only the files that are actually missing are written; files that already")
	fmt.Fprintln(stdout, "exist at the user level are inherited, never replaced.")
}
