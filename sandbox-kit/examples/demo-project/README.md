# greet

A tiny Go program with one bug in it. `make test` fails, and the agent's job
is to make it pass.

## The task

Run `make test`, read what it says, and fix `Greeting` so that the test
passes. Do not change the test.

## Why it exists

It is a sandbox demonstration. The agent has to read a file, run a command,
edit code, and run the command again — every one of those inside the sandbox
boundary, with the host reachable only through the workspace directory.

## What the agent will do to it

The agent stops once, before it edits: `write_file` and `edit_file` are in the
`ask` column of the policy, so the sandbox asks you first. **Approve it** and
the agent finishes the job — the file changes on your host, because the
workspace *is* a directory on your host.

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

## The configuration this project carries

`.openheron/` is not empty, and that is the point:

- `agents/main.md` and `agents/task.md` — this project's own agents. The
  default pair works the same way, but on a requirement this small the default
  entry agent can end a turn with the diagnosis done and the fix not yet
  delegated. This project's entry agent delegates the whole job in one order.
- `policy/permissions.yaml` — `execute` is not in the `ask` column, so the
  agent can run `make test` without a prompt. A real workspace would keep
  `execute` there: a shell command has consequences, and the agent should ask
  before it runs one.

★ `openheron init` writes what is missing and keeps what is there — so these
files survive, and the ones it fills in arrive on the first start.
