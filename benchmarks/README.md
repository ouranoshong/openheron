# The cost benchmark: strong model decomposes, cheap model executes

What this measures: **how much money does the multi-agent topology save, and at
what price in quality?**

The hypothesis under test is not "the cheap model is cheaper". It is
"decomposing the work and delegating the execution is cheaper than letting one
strong model do everything" — and that only holds if the coordination does not
eat the saving.

## The three arms

| Arm | Topology | Models | Role |
|---|---|---|---|
| **A** `a-max-parent-flash-workers` | parent + workers | parent `qwen3.8-max`, worker `qwen3.8-flash` | the hypothesis |
| **A′** `a2-max-parent-flash-workers-cited` | parent + one worker | parent `qwen3.8-max`, worker `qwen3.8-flash` | the same shape, but the worker reports conclusions and citations |
| **A″** `a3-max-parent-flash-workers-scouted` | parent + scout + worker | parent `qwen3.8-max`, scout and worker `qwen3.8-flash` | the parent delegates the investigation too |
| **B** `b-max-solo` | one agent | `qwen3.8-max` | the baseline: one strong model does everything |
| **C** `c-flash-solo` | one agent | `qwen3.8-flash` | the control that keeps the conclusion honest |

★ **C is not optional.** Without it, "A is cheaper than B" may only mean
"flash is cheaper than max" — the decomposition would have earned nothing. C
shows what the cheap model does on its own, and the interesting question is
whether A beats C *at C's quality*.

## Why the two parameters decide it

The saving is not a matter of taste; it falls out of two measurable numbers:

- **α** — the share of the work that gets delegated
- **κ** — the coordination overhead (the parent's decomposition, the parent's
  reading of the workers' output, the extra rounds) ÷ the strong model's solo
  cost

With `r` = the flash/max price ratio (≈ 0.1):

```
Cost_B = W
Cost_A ≈ (1−α)·W·(1+κ) + α·W·r
break-even: α* = κ / (1 + κ − r)
```

★ So the headline of the report is **α\***: the delegation share above which
the multi-agent topology is cheaper. At κ = 0.3 that is about 25 % — the
delegation has to be worth more than the coordination, and roughly by the same
order of magnitude.

## The price table

`arms.yaml` carries it (CNY per million tokens, 2026-10-31):

| Model | input | output | cached input |
|---|---|---|---|
| `qwen3.8-max` | ¥12 | ¥36 | ¥1.5 |
| `qwen3.8-flash` | ¥0.8 | ¥2.7 | ¥0.1 |

★ **The table is checked against the provider's own bill**, not taken on
trust: every rate back-solves from the billed amount and the billed token
count, with zero residual on all six. If a rate here is ever wrong, the
benchmark's headline is wrong with it.

★ **Three things the table says that are easy to get wrong:**

1. **The saving is in the output.** The output is 13× more expensive at max, so
   what has to move to the workers is their *output* tokens. The parent's
   context is cheap to re-read — a cached input at max is ¥1.5/M.
2. **The coordination is charged at the strong model's price.** Every
   decomposition and every integration pass is a max call.
3. **The cache-hit rate is a variable, not a constant.** The two arms have
   different cache behaviour, so it has to be measured, not assumed.

The report carries both the absolute ¥ and the ratio to arm B, because prices
move and the ratio survives that.

## The tasks

`tasks.yaml` — 15 tasks over a small fixture tree (`fixtures/work/`), in
four kinds:

| Kind | Count | What it is | What it is expected to show |
|---|---|---|---|
| `exec` | 5 | execution: read a value, count files, write a file, run a command, locate a symbol | A wins: the work is the workers' to do |
| `decompose` | 4 | the parent has to split the work and hand it out | A wins, but κ is what decides by how much |
| `big` | 3 | the work is large enough for the delegation to matter | ★ **A wins**: the same ratio, applied to more work |
| `judge` | 3 | a judgement call | ★ **B wins**: delegating a judgement buys nothing, the parent still decides — the coordination is pure overhead |

★ **The third kind is the point of the set.** A benchmark that averages over
everything hides the boundary of the pattern: "delegate the work, not the
decisions" is the rule, and the `judge` tasks are where it shows.

★ Every task has a **checkable** end state (a file, a number, a file:line, a
keyword in the final answer). That is what makes the cost comparison mean
something — a run that is 40 % cheaper and wrong is not a saving.

## Running it

Prerequisites:

- A server with the qwen endpoints configured (`llm/<provider>.yaml` with
  `qwen3.8-max` and `qwen3.8-flash`) and a **scratch** config root.
- The `agents/` directory of that config root belongs to the benchmark: the
  runner rewrites it on every arm.

```bash
# 1. a scratch config root with the models configured
cp -r .openheron .openheron-bench     # then add the qwen endpoints

# 2. the server, pointed at the scratch root and at a scratch project root
OPENHERON_CONFIG_DIR=$PWD/.openheron-bench ./bin/openheron --project-root $PWD/benchmarks/fixtures/work

# 3. the benchmark
bun run benchmarks/run.ts \
  --base http://127.0.0.1:18099 \
  --config $PWD/.openheron-bench \
  --work $PWD/benchmarks/fixtures/work \
  --repeats 3
```

★ `--work` is the project root the agents may touch: the runner resets it
from `fixtures/work/` before every task, so the tasks start from the same tree.

Flags: `--arm <id>` and `--task <id>` take a **comma-separated list**
(`--arm a2-...,b-max-solo`), `--kind <kind>` takes one kind, `--repeats N`
(default 3 — the model is stochastic, so the report uses the mean and the
median matters more than any single run), `--timeout <ms>` (default 600000 —
a guard against a hung model, not a budget; the run is aborted when it fires).

Output: `benchmarks/out/report.json` (every run) and a summary on stderr.

`benchmarks/analyze.ts` is the second half of the tool: it reads the
transcripts and splits each run into its phases, prints the per-task medians
and the per-arm ratios, and **asserts two things on every run** — that the
staged sum equals the cost the runner recorded, and that the decomposition
identity holds. It exits non-zero when either fails.

```bash
bun run benchmarks/analyze.ts --report benchmarks/out/report.json --config $PWD/.openheron-bench
```

## Where the numbers come from

★ **From the transcript, not from an estimate.** Every assistant message
carries the endpoint's own token accounting (`ResponseMeta`) and the agent that
produced it. The API already splits the usage per agent on the history path
(`api/v2_sessions.go` — `sessionUsageByAgent`), and the runner reads that same
split: prompt tokens, completion tokens, and **cached** prompt tokens, per
agent.

★ **The model is the one the agent ran on, not the one the arm table says it
should run on.** The transcript carries it per message: the agent's name is on
the message and the model is in its metadata (`api/v2_convert.go` — `name` and
`metadata.model`). The arm table says which model an agent *should* use; the
message says which one it *did*. This benchmark exists to compare what the
strong model costs against what the cheap one costs, and that comparison is
only as good as the model attribution behind it — so the attribution is read,
not assumed. The arm table's mapping is the fallback for the rare case of one
agent running on two models, where the token bucket cannot be split; that case
is reported (`modelConflicts`) rather than priced silently.

★ **The stages come from the same transcript.** `analyze.ts` splits a run into
the phases the arrangement has — `analyze` (the parent works out what has to
happen), `handoff` (the parent writes the task order), `child` (the worker
executes), `accept` (the parent checks the result) — by the position of the
parent's `task` tool calls. The API's per-agent split cannot answer "which
phase", and the phase split is what the finding is made of. The two agree:
the staged sum must equal the recorded cost, and that check is one of the
analyzer's two assertions.

★ The quality is the check, not a judge: the checks are strings and files.
The `judge` tasks are the weak point — a keyword check is a proxy, and a real
run should add a rubric with a fixed judge model.

## What would invalidate the result

1. ★ **The decomposition quality.** α and κ are properties of the parent
   model, not of the topology. A different parent version can flip the
   conclusion — so the report carries the model version.
2. ★ **The workers' output quality.** If it collapses, the parent reworks it
   and the rounds multiply: the cost goes up, not down.
3. ★ **The cache-hit rate** differs between the arms and is measured, not
   assumed.
4. ★ **Whether the parent delegates the work at all.** In the one-task shape
   (A″) the saving depends on the parent choosing to hand the investigation
   to the scout; when it decides to read the code itself, that task's
   delegation loses. This is the parent's discretion, so it is a variance
   source, not a constant — the arm needs a median over repeats.
5. ★ **★ Whether the parent finishes the shape.** A shape with two
   delegations can be abandoned half-way: on one `two-file-change` run the
   parent investigated twice and then reported the investigation as the
   answer, without ever delegating the implementation. The prompt says what
   the steps are; the model decides how many to take. A shape that depends on
   a *sequence* of delegations needs the second one to be non-optional, or it
   needs a mechanism that enforces it.

## What the sweep showed

One repeat over all fifteen tasks (the report is `out/report.json`; the two
judgement failures in that run were **check** bugs, not model failures — one
expected a stale file count, one compared case-sensitively — and both are
fixed, so the table below is the corrected verdict):

| Arm | Total | Passed | Mean rounds | Ratio to B |
|---|---|---|---|---|
| A strong parent + cheap workers | ¥3.9360 | 15/15 | 8.9 | **1.002** |
| B one strong model | ¥3.9264 | 15/15 | 6.8 | 1.000 |
| C one cheap model | ¥0.0372 (5 tasks) | 5/5 | 3.6 | 0.095 on those five |

★ **A and B are a tie.** A won seven tasks and B won eight — there is no
systematic win. By class:

| Class | A | B | A/B | rounds A/B |
|---|---|---|---|---|
| exec | ¥0.452 | ¥0.386 | 1.171 | 4.2 / 3.0 |
| decompose | ¥1.248 | ¥1.036 | 1.204 | 11.5 / 8.2 |
| big | ¥1.178 | ¥1.295 | **0.910** | 16.0 / 10.0 |
| judge | ¥1.058 | ¥1.209 | **0.875** | 6.3 / 8.0 |

★ **The finding that explains the tie: the coordination is charged at the
strong model's price.** The parent's extra rounds — the decomposition and the
integration — are max-rate calls, and the workers' rounds are flash-rate
calls. So the discount the workers earn is paid for in the currency the parent
spends. A decomposition that adds three rounds at ¥36/M does not repay a
worker doing the same work at ¥2.7/M unless the work moved is much larger than
the coordination.

That is the break-even from the design section, measured: **α\* was not reached
on these tasks.** The big class (16 rounds) and the judge class came out ahead
for A; the exec class (4 rounds) and the decompose class (11.5 rounds) came
out behind. The threshold sits somewhere around ten rounds of real work — and
the classes where A won are the ones where the parent's own thinking was
replaced by a cheap worker's execution, not the ones with the most work to
move.

★ **The control arm is where the real finding is.** C — one cheap model doing
everything — was **12× cheaper** than A on the same five tasks, and passed
them all. Then it **timed out on `find-every-caller`**, the first task that
needs a real search. The cheap model is not slightly worse at this work; it
falls off a cliff, and the cliff is exactly where the cost advantage would
have been spent.

★ **The cost is not where the delegation saves it — and the input cache is
exactly why.** The cost splits three ways, and the split is the finding:

| Arm | cached input | fresh input | output | total |
|---|---|---|---|---|
| A | ¥1.1223 (28.5 %) | ¥1.4605 (37.1 %) | ¥1.3532 (34.4 %) | ¥3.9360 |
| B | ¥1.4390 (36.7 %) | ¥1.2519 (31.9 %) | ¥1.2355 (31.5 %) | ¥3.9264 |

★ **In arm A the workers' execution is 3.1 % of the cost.** Everything else —
96.9 % — is the parent: reading the workers' output, and writing the
integration. The workers' own output (flash-priced) is ¥0.043 of the ¥3.94.

★ **And the cache cannot touch the part that hurts.** The cached input is 8×
cheaper (¥1.5/M against ¥12/M), and it covers exactly the context the parent
re-reads — the part it would have paid for anyway. The input that a
delegation *has* to read — the workers' report — is **fresh by definition**:
new content, never in the cache, full price. It is the single largest item in
arm A (37.1 %).

★ So the saving a delegation can buy is the workers' **output**, and the price
it pays is the parent's **fresh input plus its own output** — both at the strong
model's rate. The discount on repeated context, which is what the cache
actually gives, lands on both arms and cancels out of the comparison.

★ **This is a concrete, measurable lever.** The factory's task order asks the
worker to report "the real diff and the complete output" — that report is
exactly the fresh input the parent has to read and pay full price for. A
worker that returned a conclusion and a *reference* would cut the largest
item in arm A. The benchmark can measure that: same tasks, same arms, a task
order that asks for a summary instead of the transcript.

★ **One repeat is noise.** The same task in two runs of the same arm moved by
2× (B's `count-go-files`: ¥0.0515, then ¥0.1082 — and this sweep's B run
measured ¥0.0656). `--repeats 3` is the minimum for a claim, and the median
matters more than the mean.

## The other shape: one coding task, two roles

The sweep above measures **many tasks, decomposed and handed out**. There is a
second shape, and it is the one that looks like the question people actually
ask: **one requirement, one strong model plans it, one cheap model implements
it, the strong model does not write a line of code.**

The two shapes have different economics, and the numbers below do not transfer
between them. Here the parent's job is not to split work — it is to decide, and
to decide well. The interesting question is whether the cheap implementation
pays for the strong decision.

### The arms

| Arm | Topology | What the worker reports |
|---|---|---|
| **A′** `a2-max-parent-flash-workers-cited` | parent + one worker | conclusions with `file:line` citations, not the diff |
| **A″** `a3-max-parent-flash-workers-scouted` | parent + read-only **scout** + one worker | the scout's findings first, then the worker's citations |

★ **A″ puts the investigation itself on the cheap model.** The parent never
reads the code: a read-only scout reads it and reports the facts with
citations, and the parent decides from those. This is the shape that matches
"the strong model understands the requirement and the cheap one does the
typing" — and it is the only one of the two where the parent does not pay for
reading the code at the strong model's rate.

### What the measurement showed

Three coding tasks, `--repeats 3`, **all three arms in one sweep** so the
baseline is the same batch for every row:

| Arm | Total (median of medians) | Passed | Ratio to B | Parent's `analyze` phase |
|---|---|---|---|---|
| **A″** scout | ¥0.6963 | **6/9** | **0.587** | **¥0.0000 /run** |
| **A′** citations | ¥0.8719 | 9/9 | **0.735** | ¥0.0924 /run |
| **B** one strong model | ¥1.1862 | 9/9 | 1.000 | ¥0.1273 /run |

★ **In this shape the delegation does pay — and A″'s mechanism is visible in
the stages.** The parent's `analyze` phase in A″ is **exactly zero**: it reads
no code. Everything the parent knows about the code arrives through the
scout's report. In A′ the same phase costs ¥0.0924 per run, which is the
parent reading the files itself.

Per task (median of 3; A″'s `add-a-method` has one completed run out of three):

| Task | A″ | A′ | B | A″/B | A′/B |
|---|---|---|---|---|---|
| `write-a-file` | 0.0672 | 0.0740 | 0.1013 | **0.663** | **0.731** |
| `two-file-change` | 0.3449 | 0.4643 | 0.5703 | **0.605** | **0.814** |
| `add-a-method-to-three-types` | 0.2842 | 0.3335 | 0.5145 | **0.552** | **0.648** |

★ **Three findings that the totals hide.**

1. **The worker's report is the parent's fresh input.** The parent pays full
   price for whatever the worker hands back, because a report is new content
   that was never in the cache. A′ asks for conclusions with citations, and
   the parent's acceptance re-reads drop from **2.4 times per run** (the arm
   that pastes the whole diff) to **0.22** — the parent's own file-reading is
   what the citations remove.

2. **★ The scout's report is longer than the worker's — and that is not
   what makes it expensive.** Across A″'s completed runs the scout returned
   **16,633 characters** and the worker **6,921** — the investigation report
   is **2.4×** the implementation report. It looks like the expensive
   stage, and it is not: **the report's length turned out to be a red
   herring.** ★ An arm that reports *where the change has to go* instead of
   *what the code currently is* (A″′, `a4-max-parent-flash-workers-scouted-tight`)
   did not get cheaper: the scout's report per run went from 2,376 to 1,934
   characters on the same task, and the parent's cost stayed flat.

   ★ **What actually costs is the parent's own output.** Per delegated run
   the parent's spend splits:

   | | cached (re-reads) | fresh (new content) | **output (its own reasoning)** |
   |---|---|---|---|
   | tokens | 22,752 | 5,087 | **2,980** |
   | ¥ | 0.0341 | 0.0610 | **0.1073** |

   ★ **Half the parent's cost is its own output** — 2,980 tokens at ¥36/M,
   which is **24× the cached rate**. Reading the report is a *cached* read
   (¥1.5/M) and is nearly free; the parent re-reads its own context 22,752
   tokens a run at that rate. ★ So the lever is not "make the parent read
   less" — it is **"make the parent think less and write less"**: fewer
   turns, and a shorter work order. A prompt that makes the parent more
   careful makes it *more* expensive, which is exactly what A″′ measured.

3. **★ A″ is cheaper but not equally reliable: 6/9 against B's 9/9.** Two of
   the three failures are the endpoint's own (a request timeout and a network
   error) and would have hit any arm. The third is the shape's: on
   `two-file-change` the parent called the scout **twice** and then reported
   the scout's findings as its answer — it never delegated the
   implementation, so nothing was changed. The same run shows the parent
   calling the scout twice and *then* implementing (`scout, scout, task`), so
   the parent is not following a fixed sequence. ★ The prompt says "have scout
   investigate, then have task implement it"; the model decides how many
   times to investigate, and one investigation is enough to look like the job
   is done. **A shape with two delegations needs the second one to be
   non-optional.**

★ **And the shape has a cost the ratio does not show: wall clock.** A″ makes
two delegations instead of one, so it runs longer: an A″ run exceeded the
300 s timeout that the earlier sweep used, and the sweeps since use 600 s. An
arm that delegates twice needs roughly twice the time.

### A″′: the fix that was not a fix

★ A″′ (`a4-max-parent-flash-workers-scouted-tight`) is A″ with the two
defects above addressed: the scout reports **where the change has to go**
instead of what the code currently is, and the parent is told that the
implementation step is not optional. One probe (3 tasks, 1 repeat, B in the
same sweep) came out at **0.603** against B — indistinguishable from A″'s
0.587, and the scout's report did not get shorter either (1,934 characters
against 1,938 on the same task).

★ **The reason is finding 2 above: the report's length was never the cost.**
The parent's own output is, and a prompt that makes the parent more careful
makes it *more* expensive. ★ **A negative result, and the useful one: to make
this shape pay, shorten the parent's work order and cut its turns — do not
give it more instructions.** A″′'s instructions did exactly that, and the cost
went up by 8 %.

★ One of A″′'s three runs failed a check, and **the check was the bug**: it
required the literal text `Flush()` in `main.go`, which prescribes a no-argument
signature the prompt never asked for. The worker wrote
`Flush(w io.Writer) error` and called `c.Flush(os.Stdout)` — a better design,
marked wrong. The check now looks for `.Flush(`, which matches either. ★
Every run that passed before still passes: all of them wrote a no-argument
call, which `.Flush(` also matches.

### Why the two shapes differ

In the many-tasks shape the parent's overhead is a **fixed** cost per task
(decompose once, integrate once) and the delegated work is **many small
pieces** — so α can be large and κ small. In the one-task shape the parent's
overhead is **the whole decision**, and the delegated work is one piece, so
the saving is a single price ratio and the overhead is a single investigation.

★ **That is why the same two models flip their order between the shapes.** In
the many-tasks shape A ties B (1.002) because the parent's extra rounds cost
as much as the workers save. In the one-task shape the parent's extra work is
*reading the code*, and moving that reading to the cheap model is worth more
than it costs — as long as the parent actually delegates it.

## What the numbers cost to trust

The cost column is only as good as the accounting behind it. Three things were
found by checking the benchmark's own output against the provider's bill, and
all three are now properties of the benchmark rather than surprises.

★ **The price table is verified, not assumed.** Every rate in `arms.yaml`
back-solves from the billed amount and the billed token count with zero
residual. The bill is the arbiter; the price page is a starting point.

★ **A run that never finished is not a free run.** The provider bills every
call it made, and the calls that never came back — a request that hangs and
then fails — are billed without ever producing a message, so no local
accounting can see them. A failed run's `cost` is a **lower bound**, not the
whole: the report marks it `costIncomplete`, and the analyzer says so on the
arm's line. Reporting a zero would read as "this run was free", and it was
not.

★ **The free tier is an account-level, one-time resource.** The October bill
reproduces to within 0.4 % of "the usage from a certain moment onwards" — the
usage before that moment was covered by the free quota and was never charged.
★ It hit the **early** runs of a sweep and nothing else, so it moves the
absolute ¥ of a campaign without moving the **ratio** between arms: it is
charged to whoever spent it first, and the arms interleave. A ratio is the
number that survives it.

★ **A client that gives up does not stop the run.** The connection subscribes
to the run; it does not consume it, so a connection that dies leaves the run
going (`api/v2_run.go`). That is right for a user who closes the browser and
expects the agent to keep working, and it is wrong for a benchmark that has
given up — it would keep paying for calls whose answers nobody reads. The
runner therefore calls `POST /v2/sessions/{id}/abort` when it times out. The
abort is idempotent, so it is safe on a run that already ended.

## Running out of quota mid-sweep

★ The sweep can die halfway: the endpoint's free tier has a quota, and a
403 "Free quota exhausted" turns every later run into an empty one. The
runner records a `RUN_ERROR` frame as the run's error (a run that produced
nothing to grade is an error, not a failed answer), so a truncated sweep is
visible in the report rather than looking like a task the model could not
do.

The runs before the quota ran out are still valid — the report keeps them
(`benchmarks/out/report.json`), and `turns > 0` tells a real run from an
empty one.

★ **A quota that runs out is not a modelling result.** The runs it covered
were free, and the runs after it were not; averaging the two into one arm
total mixes two pricing regimes. When a sweep is truncated by the quota, the
runs before and after it belong to different cost regimes and should be read
separately — the bill is what tells them apart, and the cut-off is visible in
it as the point where the accounted usage starts matching the billed one.

## The known gap this benchmark does not need

The live run does not account the usage per agent — `usage.byAgent` exists
only on the history path (a known issue in the developer manual). The
benchmark reads the history, so it does not care. Fixing the live path
would still be worth it: the agent filter's chips show no cumulative spend
during a live turn.

★ The benchmark reads **both** paths on purpose: the API's per-agent usage
for the total (the server's own view of the session), and the transcript for
the phases and for the model each message ran on (which the per-agent split
cannot give). The two are cross-checked against each other on every run, so a
disagreement between them shows up as a failed assertion rather than as a
wrong number.
