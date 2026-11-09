# greet

A tiny Go program with one bug in it. `go test ./...` fails, and the agent's
job is to make it pass.

## The task

Run `go test ./...`, read what it says, and fix `Greeting` so that the test
passes. Do not change the test.

★ **The test command is `go test ./...`, not a build tool's target.** This
project runs **inside the sandbox**, and the base image owes the platform
only `bash`, `git` and a CA bundle — no `task`, and no `make` either. The
demo therefore asks for the toolchain the program itself needs (Go), which
the image has because the agent has to edit Go code to do the job at all.

## Why it exists

It is a sandbox demonstration. The agent has to read a file, run a command,
edit code, and run the command again — every one of those inside the sandbox
boundary, with the host reachable only through the workspace directory.

## What the agent will do to it

The agent asks before it acts: `write_file`, `edit_file` and `execute` are
tools nobody names, and the default tier is **ask** — so the sandbox asks you
first, once per command, and once before it edits. **Approve them** and the
agent finishes the job — the file changes on your host, because the workspace
*is* a directory on your host.

```
$ diff -u main.go ~/demo-ws/main.go
-	return "Hello, " + name
+	return "Hello, " + name + "!"
```

## The mixin that came with it

`openheron-deepseek/` is a copy of the DeepSeek mixin, so that this
directory is self-contained: a mixin is a declaration, and a declaration is a
directory holding one YAML file. Point `--kit` at it and the runtime builds it
on the spot — no image, no registry.

## Declaring it

★ **This directory carries an `sbxenv.yaml`** — the environment file that
declares the sandbox: the agent, the Kits, and the workspace.

```sh
sbx env plan      # what would change
sbx env create    # create the sandbox
```

★ **The format is the documented one.** `sbxenv.yaml` is Docker Sandboxes'
own environment file, and its
[field reference](https://docs.docker.com/ai/sandboxes/configuration/environment-files/)
covers every key used here — `schemaVersion`, `name`, `agent`, `kits` and
`workspace`. The file is not an openheron invention, so a file that works
here works with the rest of the toolchain.

★ **The workspace is the important part.** It is what puts your project inside
the sandbox: the runtime mounts the directory, and the agent works on your
files rather than on a copy.

★ **The file stays out of the sandbox's reach.** `sbx` mounts it read-only,
because it declares what runs on your host.

## The configuration this project carries

★ **None — and that is the point.** There is no `.openheron/` in this
directory, and `openheron init` writes the whole thing on the first start:
the two agents, the model configuration, the MCP example and the server
settings.

★ **A project that ships its own configuration keeps it.** `init` writes what
is missing and keeps what is there, so a project that wants its own agents or
its own tool configurations just carries those files — this one does not need to.

★ **What the default tier means for the demonstration.** `write_file`,
`edit_file` and `execute` are tools nobody names, so they ask — the agent asks
before it writes a file and before it runs a command, and running
`go test ./...` is a command. **Approve them** and the agent continues. A demonstration that
needs nobody at the keyboard would name `execute` in a tool file's `allows:`
list, and that is the user's call, not the repository's.
