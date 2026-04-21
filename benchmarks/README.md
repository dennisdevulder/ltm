# cold-vs-warm benchmark

Measures how much context-transfer a Core Memory Packet buys in a realistic
"you hit a wall, your agent is replaced, continue" scenario.

**Hypothesis:** an agent handed a well-formed CMP solves a task with
dramatically fewer tokens and zero repeated dead-end attempts, compared to
the same agent starting cold with only a terse task description.

## Design

Each benchmark task is a triple:

```
tasks/<slug>/
├── task.md           # the prompt the agent always sees (terse; the re-briefing they'd get cold)
├── packet.json       # the CMP a prior session would have left behind
└── verifier.py       # deterministic checker: parses agent output, returns
                      # (solved: bool, repeated_dead_end: bool, respected_locks: bool)
```

We run each task **twice**:

| Condition | Input to the agent |
|---|---|
| **cold** | `task.md` only |
| **warm** | `task.md` + the block produced by `ltm resume <id>` on `packet.json` |

A third condition `oracle` (gives away the answer) is available as a ceiling
sanity check — not part of the reported comparison.

## Metrics

Per task, per condition, per run:

| Metric | How measured | Why it matters |
|---|---|---|
| `input_tokens` | Anthropic API `usage.input_tokens` | Cold includes less input but will churn more |
| `output_tokens` | Anthropic API `usage.output_tokens` | Warm agent should need less reasoning |
| `total_tokens` | sum of above, across all turns in the run | The headline number |
| `wall_seconds` | client-side | Secondary — cost, not quality |
| `solved` | verifier.py | Must pass for the run to count |
| `repeated_dead_end` | verifier.py — did the agent retry something the packet marked `failed`? | Direct measure of the packet's value |
| `respected_locks` | verifier.py — did the agent accept `locked: true` decisions? | Tests whether framing as "locked" actually works |
| `turns` | count of model calls | Warm should resolve in fewer turns |

## Reported headline

> **Token savings**: `1 − (tokens_warm / tokens_cold)`, averaged across tasks.
> **Dead-end avoidance**: `% of runs where repeated_dead_end == false`, warm vs cold.
> **Lock respect**: `% of runs where respected_locks == true`, warm vs cold.

Aim for n ≥ 5 runs per condition per task (same prompt, different seed) to
smooth stochastic variance; report mean + stderr.

## Task selection criteria

Good tasks for this benchmark have **all** of:

1. A clear success condition a verifier can check deterministically.
2. At least one *plausible-looking wrong path* that burns 2–5k tokens before hitting a wall (otherwise the packet doesn't save anything).
3. A constraint or decision that a cold agent will naturally re-derive, but a warm agent can skip.
4. No dependency on external services beyond the LLM API itself (no running GPU, no real network, no sandbox).

Bad tasks: anything solvable in one token (warm and cold both cheap);
anything requiring real code execution (confounds tokens with env setup).

## Starter tasks

| slug | trap | what the packet carries |
|---|---|---|
| `vulkan-moltenvk` | Cold agent tries fighting MoltenVK for Vulkan video encode; burns tokens on build configs that can't work | Locked decision "abandon macOS for this feature"; attempt "MoltenVK lacks VK_KHR_video_encode_queue" |
| `rails-kamal-ghcr` | Cold agent proposes fine-grained PAT; pushes fail; suggests re-scoping repeatedly | Attempt marked failed: "fine-grained PAT with all permissions still denied on first-push"; locked decision: "use classic PAT" |
| `llama-cpp-metal` | Cold agent tries Metal backend paths already known dead | Attempts: `brew install llama.cpp` OOMs; `GGML_METAL=1 make` link error |
| `sqlite-wal-timeout` | Cold agent tunes `busy_timeout`; bottleneck is actually WAL checkpoint | Decision "bottleneck is checkpoint not lock contention"; attempts `PRAGMA busy_timeout` changes (failed) |
| `auth-devise-omniauth` | Cold agent reaches for Supabase Auth, then has to unwind | Locked decision "Devise + OmniAuth, not Supabase Auth"; rationale "two auth systems adds state to manage" |

## Running it

The harness is a small Python script calling `anthropic` v0.69+. Stub layout:

```
benchmarks/
├── README.md                       (this file)
├── run.py                          # main driver
├── tasks/<slug>/{task.md,packet.json,verifier.py}
└── results/<run-id>/{log.jsonl,summary.md}
```

`run.py` pseudocode:

```python
for task in tasks:
    for condition in ["cold", "warm"]:
        for seed in range(N):
            messages = build_prompt(task, condition)
            response = anthropic.messages.create(
                model="claude-sonnet-4-6",
                max_tokens=4096,
                messages=messages,
            )
            result = task.verifier(response.content)
            log({
                "task": task.slug,
                "condition": condition,
                "seed": seed,
                "input_tokens":  response.usage.input_tokens,
                "output_tokens": response.usage.output_tokens,
                "solved": result.solved,
                "repeated_dead_end": result.repeated_dead_end,
                "respected_locks":   result.respected_locks,
            })
```

Not yet implemented — tasks `tasks/vulkan-moltenvk/` and `tasks/rails-kamal-ghcr/` are seeded as worked examples below.

## Why this is worth running

Three concrete things it proves or disproves:

1. **The packet format is load-bearing, not decorative.** A token-savings % that's statistically zero means we're not actually transferring useful state — the spec needs rework. Non-zero means the shape works.
2. **Locked decisions get respected.** If warm agents still re-litigate locked items, the framing language needs to change (Reflexion showed wording matters).
3. **Dead-end avoidance is the main win.** If warm agents still re-attempt known-failed paths, the `attempts[].learned` field is undervalued in the rendered resume block and should be moved more prominently.

A single clean result on one task is publishable as a blog-post diagram;
five tasks with tight error bars is a defensible v0.2 motivation slide.
