# ltm

**Portable understanding for AI work sessions.**

`ltm` moves the *intent and state* of a work session between machines, models, and agents — without dragging along your configuration. Your CLAUDE.md, your skills, your tool setup stay yours. Only the understanding of what you're doing and why travels.

## The problem

You're mid-pivot. You hit a wall on one machine — wrong OS, wrong GPU, wrong model — and jump to another. The new environment has no memory of what you were doing, why you abandoned the last approach, or what you already ruled out. You spend an hour re-briefing the model. It makes the same mistakes you already solved for.

Existing "AI memory" tools solve this for a single agent on a single machine. None of them move cleanly between providers or hosts, and most either require enterprise contracts or bundle so much configuration that they break on the far side.

`ltm` is the smallest possible thing that solves this:

- A **protocol** — the [Core Memory Packet](./SPEC.md) — that captures intent, decisions, and open questions in ~5 KB of JSON.
- A **CLI** — `ltm push` / `ltm pull` / `ltm auth` — that moves packets between machines.
- A **server** — self-host on any VPS, or federate between hosts. No cloud requirement. No enterprise tier.

## Design principles

1. **Intent is portable; configuration isn't.** We never ship your CLAUDE.md, skills, prompts, or tool configs. Your setup is your business.
2. **Self-host or nothing.** There is no hosted tier that unlocks features. If it doesn't run on a $5 VPS, it's not done.
3. **Model-agnostic.** A packet written by Claude is readable by GPT, Gemini, or the next thing. No vendor fields.
4. **Spec first, code second.** The [protocol](./SPEC.md) is the product. Implementations follow.
5. **Redact aggressively.** Packets are expected to travel between machines, teams, and (eventually) organizations. Secrets and local state never ride along.

## Status

Pre-alpha. The spec is drafting. No code yet. Watch this repo if you want to help shape the protocol.

## License

TBD — open-source permissive (likely Apache 2.0 or MIT).
