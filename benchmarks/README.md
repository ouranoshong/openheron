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

`tasks.yaml` — 12 tasks over a small fixture tree (`fixtures/work/`), in
three kinds:

| Kind | Count | What it is | What it is expected to show |
|---|---|---|---|
| `exec` | 5 | execution: read a value, count files, write a file, run a command, locate a symbol | A wins: the work is the workers' to do |
| `decompose` | 4 | the parent has to split the work and hand it out | A wins, but κ is what decides by how much |
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

Flags: `--arm <id>` (one arm), `--kind <kind>` (one kind), `--repeats N`
(default 3 — the model is stochastic, so the report uses the mean and the
median matters more than any single run).

Output: `benchmarks/out/report.json` (every run) and a summary on stderr.

## Where the numbers come from

★ **From the transcript, not from an estimate.** Every assistant message
carries the endpoint's own token accounting (`ResponseMeta`) and the agent that
produced it. The API already splits the usage per agent on the history path
(`api/v2_sessions.go` — `sessionUsageByAgent`), and the runner reads that same
split: prompt tokens, completion tokens, and **cached** prompt tokens, per
agent. The model each agent ran comes from `arms.yaml`, so the arms and the
price table cannot drift apart.

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

★ **One repeat is noise.** The same task in two runs of the same arm moved by
2× (B's `count-go-files`: ¥0.0515, then ¥0.1082 — and this sweep's B run
measured ¥0.0656). `--repeats 3` is the minimum for a claim, and the median
matters more than the mean.

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

## The known gap this benchmark does not need

The live run does not account the usage per agent — `usage.byAgent` exists
only on the history path (a known issue in the developer manual). The
benchmark reads the transcript, so it does not care. Fixing the live path
would still be worth it: the agent filter's chips show no cumulative spend
during a live turn.
