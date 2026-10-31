# Filesystem Backend Design — a bounded fork of `eino-ext` local

> Status: **implemented.** `agentkit/fsx` replaces
> `github.com/cloudwego/eino-ext/adk/backend/local@v0.2.2` in both call sites
> (`agentkit/build/snapshot.go:56`, `agentkit/build/skill.go:96`). §1 describes the
> upstream code that was removed, so it reads as history; §5 points at where each piece
> lives now.
>
> Scope: the filesystem **backend** (`ls`, `read_file`, `glob`, `grep`, `write_file`,
> `edit_file`, `execute` all reach the disk through it). Not in scope: the middleware
> that renders those tools, the tool descriptions, and the approval gate.

## 1. The problem

The tools had no boundary of either kind: **no write boundary** (a path from the model
went to `filepath.Clean` and then to the disk) and **no bound on how much was read**.
`ls` and `glob` take a path from the model, and upstream does nothing with it except
`filepath.Clean`:

| # | Gap | Upstream |
|---|---|---|
| 1 | An empty path means **`/`** | `local.go:39` `const defaultRootPath = "/"`, `local.go:76-78` |
| 2 | `ls` refuses everything but an absolute path, so a model that omits it always lands on `/` | `local.go:80-83` |
| 3 | `glob` walks **the whole subtree** with no depth limit, no result limit, no ignore rules, no pruning | `local.go:275-306` |
| 4 | One unreadable directory aborts the entire walk | `local.go:282-287` |
| 5 | `write_file` / `edit_file` are just as unbounded — anything the model names gets written | `local.go:324-385` |
| 6 | `grep` buffers the **whole result** before parsing it, then splits it again | `local.go:352` `Output()` + `strings.Split` |

**Gap 3 is what actually broke.** On a WSL machine with `/mnt/d` mounted, a `glob` for
`*.md` was pointed at a path outside the project and spent its time in the Windows
recycle bin:

```
failed to walk directory: readdirent /mnt/d/$RECYCLE.BIN/S-1-5-21-.../xxx: input/output error
```

That is gap 3 and gap 4 together: the walk left the project, and the first I/O error it
met on the way killed the call.

`ls` has its own two costs:

- **A redundant sort.** `os.ReadDir` already returns entries sorted by file name;
  `local.go:96-98` sorts them again (O(n log n) for nothing).
- **No type and no size.** `local.go:102-104` fills only `Path: entry.Name()`. The
  middleware renders nothing but `Path` anyway (`middlewares/filesystem/filesystem.go:570-585`),
  so a model cannot tell a directory from a file and probes with more `ls` calls; a
  `node_modules` with 30k entries becomes 30k lines of context in one shot, and the
  `deep` path has **no** result-size cap (see §2.2).

### 1.1 A silent zero-result bug

`agentkit/run/input.go:93` tells the model:

```
glob(pattern="<projectRoot>/**/*.go")
```

while `local.go:300` matches `req.Pattern` against a path **relative to the walk root**.
An absolute pattern never matches. Every glob call the prompt taught the model to write
returned "no files found" — quietly.

## 2. Why the backend is the only place this can be fixed

### 2.1 `deep.Config` exposes three fields of a middleware that has twelve

`deep` builds the filesystem middleware itself:

```go
// eino@v0.9.21/adk/prebuilt/deep/deep.go:219-231
fm, err := filesystem2.NewTyped[M](ctx, &filesystem2.MiddlewareConfig{
	Backend:        cfg.Backend,
	Shell:          cfg.Shell,
	StreamingShell: cfg.StreamingShell,
})
```

`MiddlewareConfig` carries `LsToolConfig`, `GlobToolConfig`, `CustomSystemPrompt`,
`UseMultiModalRead` and the offloading knobs — none of them reachable from
`deep.Config`. `localbk.Config` has one field (`ValidateCommand`), so the backend
config is no better.

### 2.2 The cap that exists upstream is not on this path

`large_tool_result.go` offloads oversized tool results to a file, but it is wired only in
the **deprecated** struct-based constructor (`filesystem.go:208-215`), not in `NewTyped`,
which is what `deep` calls. So on our path there is no truncation anywhere: the backend
returns 30k entries, the tool joins them, the model reads them.

Conclusion: to bound `ls` and `glob` we must bound them **where they read the disk**.

### 2.3 Why not copy the middleware instead

| Blocking fact | Evidence |
|---|---|
| It imports an eino **internal** package, so a verbatim copy does not compile | `filesystem.go:32` `github.com/cloudwego/eino/adk/internal` (used at `:708`, `:1252`) |
| 1,711 production lines plus 2,752 lines of upstream tests | `filesystem.go` 1264 + `prompt.go` 270 + `large_tool_result.go` 177 |
| It owns the **prompt text** for all seven tools, in English and Chinese | `prompt.go` |

The only thing a copied middleware buys that the backend cannot is the *rendering* of
`FileInfo` (a size column, a nicer truncation marker). That is a cosmetic layer, and it
would be bought by forking the tool contract and the prompt — a much larger surface than
the behaviour it changes. See §8 for what to do if that day comes: build seven tools on
top of this backend, not a copy of the middleware.

## 3. The rule

Three layers, each owns one thing:

| Layer | Owns | Never touches |
|---|---|---|
| `agentkit/fsx` (backend) | **Behaviour**: where a write may land, how far a walk goes, when it stops, how much anything returns | The text the model sees |
| the tool layer (upstream, unchanged) | **Contract**: tool names, arguments, output shape | Which paths are reachable |
| prompt text (`agentkit/build/spec.go`, `agentkit/run/input.go`) | **Words**: where the model is told the code lives | Anything the backend can enforce |

A rule enforced in the backend cannot be talked around by a model that ignores the
prompt; a rule written only in the prompt can.

## 4. Decisions

### 4.1 Fork the backend, not the middleware

The backend is one file with one interface (`filesystem.Backend`, seven methods,
`adk/filesystem/backend.go:243-283`). The middleware is four files and a prompt. Fork the
smaller one, and keep the diff auditable: the file header names the upstream version it
came from.

### 4.2 Constants, not configuration

`maxLsEntries`, `maxGlobResults`, `maxGlobDepth`, `maxGrepMatches` and the ignore list are
package constants. Nothing has needed a different value; a knob nobody turns is a knob nobody
tests. When a real case appears, it becomes a `Config` field — not before.

### 4.3 An empty root means the working directory

`New` falls back to `.` when `Root` is empty, because the API tests build the runtime
without a project root (`api/v2_test.go:629`). It is still deterministic (the process
cwd), and it is not a config lookup: there is still exactly one way to name a root.

### 4.4 Junk names are ignored; the session directory is not

`.git`, `node_modules`, `$RECYCLE.BIN`, `System Volume Information` are skipped by name.
`.openheron/state` is **not**: uploaded files live under it and the run prompt points the
model there. The result cap protects the context instead.

## 5. The design

### 5.1 The write boundary — and why reads are not confined

```go
// agentkit/fsx/fsx.go
func (s *Local) resolve(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return s.root, nil              // never "/"
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.root, p)    // relative paths are project-relative
	}
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return p, nil                       // no containment check: see below
}
```

`resolve` is shared by every method and only decides what a request path *means*: an empty
path is the project root, a relative path hangs off it. **Reads are deliberately not
confined.** The tool set already contains `execute`, which can `cat` any file the process
can read, so confining `read_file` bought no safety — only a surprising `file not found`
for a project that lived one directory over. What the original accident needed was not a
read ban but a **bound on the walk**, and that is §5.3.

Writes are the real boundary, and they go through `resolveWrite`:

```go
func (s *Local) resolveWrite(p string) (string, error) {
	abs, err := s.resolve(p)
	if err != nil {
		return "", err
	}
	if !s.inside(abs) {
		return "", fmt.Errorf("path %q is outside the project root %q", abs, s.root)
	}
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			if !s.inside(real) {
				return "", fmt.Errorf("path %q resolves outside the project root %q", abs, s.root)
			}
			return abs, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("cannot verify that %q is inside %q: %w", abs, s.root, err)
		}
		if filepath.Dir(dir) == dir {
			return abs, nil
		}
	}
}
```

Two points that took a review to get right:

- **The ancestor walk, because the file may not exist yet.** `EvalSymlinks` cannot resolve a
  path that is not there, so the walk goes up to the deepest existing directory and checks
  that one. Without it, `write_file` on `<root>/link/new.txt` where `link` points outside
  would pass a lexical check.
- **★ It fails closed.** Only "this component does not exist" moves the walk up. Any other
  failure — permission, I/O, a symlink loop — is a directory we cannot inspect, so the
  write is refused. The first version ignored every error and returned the unchecked
  lexical path, which meant one unreadable ancestor was enough to write anywhere.

`s.roots` is the project root plus `ExtraRoots` (the session workspace and the skills
directory, which may live outside the project). Every root is made absolute and
symlink-free once, in `New`, so the comparison is between real paths.

The refusal is an **error**, not an empty result: the model gets a sentence it can act on.

### 5.2 `ls` — sort once, mark directories, cap

```go
// os.ReadDir sorts by file name already; upstream sorted a second time.
entries, err := os.ReadDir(path)
...
fi := filesystem.FileInfo{Path: entry.Name(), IsDir: entry.IsDir()}
if fi.IsDir {
	// The renderer drops IsDir, so the trailing slash is what tells the model
	// this is a directory it can list and cannot read.
	fi.Path += "/"
}
```

- `Size` and `ModifiedAt` are filled in too. The current renderer ignores them; they cost
  one `entry.Info()` per entry for at most 200 entries.
- After `maxLsEntries` the list ends with one synthetic line
  `… N more entries; list a narrower path`.
- `ctx.Err()` is checked per entry; upstream never looked at the context here.

### 5.3 `glob` — a bounded walk that survives bad directories

```go
err = filepath.WalkDir(base, func(p string, d os.DirEntry, walkErr error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if walkErr != nil {
		return nil // one unreadable subtree skips itself
	}
	...
	if d.IsDir() {
		if s.ignored(d) || strings.Count(rel, "/") >= maxGlobDepth {
			return filepath.SkipDir
		}
		return nil
	}
	if ok, _ := doublestar.Match(pattern, rel); ok {
		matches = append(matches, rel)
		if len(matches) >= maxGlobResults {
			return filepath.SkipAll // stop walking: the rest can only be noise
		}
	}
	return nil
})
```

Four properties, each answering one line of §1:

| Line | Answer |
|---|---|
| Gap 3 | depth cap, result cap, ignore list, and a walk that starts at an allowed root |
| Gap 4 | `walkErr != nil` returns nil, so the walk skips the subtree and continues |

`SkipAll` matters: hitting the cap must **stop** the walk, not keep stat-ing a million
entries to sort 200.

★ **The walk is also pruned by the pattern.** A directory whose own name cannot
be the pattern's next segment cannot hold a match, so there is no reason to walk
it: `docs/**` walks `docs/` and nothing else, and `*.go` does not walk a single
directory. The check is one segment of the pattern against the directory's name
at that depth — the walk only reaches a directory whose ancestors already
matched, so one depth is enough.

★ **And the walk has a time budget.** The caps bound what comes back; only the
budget bounds how long the turn is blocked (`walkBudget` in §5.6). `grep` gets
the same budget, because `rg` walks the tree it is given too.

### 5.4 Absolute patterns are normalised

```go
// The run prompt tells the model to pass "<projectRoot>/**/*.go", while matches are
// relative to the walk root: without this the call silently found nothing.
func (s *Local) globPattern(base, pattern string) (string, error)
```

An absolute pattern is made relative to the walk root; one that points outside the walk
root is an error naming both paths (a contradiction between pattern and path, not a
confinement rule). This fixes §1.1 and keeps the prompt's existing advice
working, so no prompt text has to change for it.

### 5.5 The other five methods

| Method | Change | Why |
|---|---|---|
| `Read` | `resolve` only | reads are open by design (§5.1) |
| `GrepRaw` | `resolve`; `--` before the pattern; `cmd.Dir = root`; results read through a pipe and capped | `-foo` is a pattern, not a flag; `rg` inherits a defined cwd; upstream buffered the whole match list twice |
| `Write` / `Edit` | `resolveWrite` | the write boundary, including through a symlink |
| `ExecuteStreaming` | `cmd.Dir = root`, `ValidateCommand` kept | relative paths in a shell command now mean the project root |

`GrepRaw` reads `rg --json` line by line and stops at `maxGrepMatches`, killing the process
instead of letting it finish a scan nobody reads. The cap is reported as one more match
(`path:0:… 200 matches is the cap…`) because the renderer prints `path:line:content` for
every entry — the same trick `ls` and `glob` use.

`GrepRaw` still shells out to `rg`, which respects `.gitignore`; `glob` does not — that
asymmetry is upstream's and stays, documented by a `shortcut:` comment where relevant.

### 5.6 Defaults

| Constant | Value | Meaning |
|---|---|---|
| `maxLsEntries` | 200 | one `ls` result, plus one cap line |
| `maxGlobResults` | 200 | one `glob` result, plus one cap line |
| `maxGlobDepth` | 16 | directories deep from the walk root |
| `maxGrepMatches` | 200 | one `grep` result, plus one cap line |
| `walkBudget` | 10s | how long one walk (glob) or one search (grep) may run |
| `ignoredNames` | `.git`, `node_modules`, `$RECYCLE.BIN`, `System Volume Information` | skipped by basename |

★ **`walkBudget` bounds the cost, not the result.** The depth and result caps
say how much comes back; the budget says how long the turn may be blocked. A
pattern over a mounted volume visits millions of entries and returns two
matches — and the tool call is synchronous, so without a budget the turn hangs
for as long as the disk takes. The walk stops when the budget is gone and says
so in its result: a truncated search that looks complete is how a model
concludes that a file does not exist.

The cap lines are a `shortcut:`: the ls/glob tools render only `FileInfo.Path`, so a cap
can only be stated as one more "entry". It is honest in the transcript and needs no
middleware change.

## 6. What changes where

| File | Change |
|---|---|
| `agentkit/fsx/fsx.go` | new: the fork, 823 lines, upstream header retained |
| `agentkit/fsx/fsx_test.go` | new: the boundary, the caps, the pattern fix |
| `agentkit/build/snapshot.go:47-60` | `fsx.New(Config{Root: projectRoot, ExtraRoots: [dataDir, skillsDir]})` |
| `agentkit/build/skill.go:96` | `fsx.New(Config{Root: dir})` — the skill list only ever reads one directory |
| `go.mod` | `eino-ext/adk/backend/local` gone; `bmatcuk/doublestar/v4` becomes a direct dependency |
| `docs/architecture.md` §2 | new L1 row for `agentkit/fsx` |

Nothing else moves: not the tools, not the middleware, not the approval policy, not the
frontend.

## 7. Testing

`agentkit/fsx/fsx_test.go`:

| Case | Asserts |
|---|---|
| `TestGlobStaysInsideRoot` | a matching file beside the root is invisible |
| `TestGlobAbsolutePattern` | `<root>/**/*.go` matches — the §1.1 regression |
| `TestGlobAbsolutePatternOutsideRoot` | an escaping pattern is refused |
| `TestGlobCapsResults` | 250 files → 200 + one cap line |
| `TestGlobCancelled` | a cancelled context stops the walk |
| `TestResolveRejectsEscapes` | `../`, an absolute outside path, `/etc/passwd`, and a symlink out are all refused for **write** and **edit** |
| `TestReadAllowsOutsideRoot` | a read of a sibling directory is allowed — the case that used to fail |
| `TestResolveWriteFailsClosed` | a write behind a directory whose permissions cannot be read is refused, and the sibling write still works |
| `TestGrepCapsMatches` | 400 hits → 200 + one cap line |
| `TestEmptyPathMeansRoot` | an empty path lists the root, not `/` |
| `TestLsMarksDirectories` | directories end in `/`, files do not |
| `TestLsCapsEntries` | 210 entries → 200 + one cap line |
| `TestExtraRootIsReachable` | an extra root is readable but is not the default |

Manual check, on the machine that produced the original error: a `glob` for `*.md` whose
path is `/mnt/d` now walks (reads are open) but comes back bounded — 200 results at most,
`$RECYCLE.BIN` skipped by name, unreadable subtrees skipped — and does not hang.

## 8. Open questions

1. **The prompt still says "always pass ABSOLUTE paths".** That is now merely redundant —
   absolute paths work, and an omitted path means the project root. Narrowing the prompt to
   "relative, or omitted" is a separate change with its own model-behaviour risk.
2. **`glob` ignores `.gitignore`.** `rg` does not. If a project's build output is not in
   the junk list, `glob` will walk it. ~~Pruning by literal pattern prefix~~ **done** —
   the walk now descends only into the directories the pattern can match. Reading
   `.gitignore` itself is still not done.
3. **Per-agent roots.** One backend instance is shared by the whole tree
   (`agentkit/build/assemble.go:32`). If a sub-agent should see less than its parent, build
   a backend per node — still a backend change, still no middleware fork.
4. **If the tool layer ever has to change** (a size column, real truncation wording), do
   **not** copy the middleware: set `deep.Config.Backend` to nil, build the seven tools on
   top of `agentkit/fsx` with `utils.InferTool`, and pass them as `cfg.Middlewares`. ~300
   lines, no `adk/internal` import, no prompt fork.
5. **The walk's base is not confined, on purpose.** `glob` and `grep` may still be
   pointed outside the project — the agent has to be able to look up a dependency's
   source in the module cache, and `execute` could `cat` it anyway. The budget is what
   keeps that from becoming a hang. If the module cache should be a root instead, that
   is a build-layer change, not a backend change.

## 9. Rollout

Single commit, no migration: the package is internal, sessions on disk are untouched, and
the two call sites construct the backend at build time. `make check` plus the checklist in
§7 is the whole gate. Upstream sync is a manual diff against
`eino-ext/adk/backend/local@v0.2.2`; the header of `fsx.go` names the version to diff
against.
