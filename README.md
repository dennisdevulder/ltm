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

## Install

```bash
# macOS / Linux via Homebrew
brew install dennisdevulder/ltm/ltm

# Or one-shot install (macOS / Linux, amd64 / arm64)
curl -fsSL https://ltm-cli.dev/install | sh
```

Build from source:

```bash
git clone https://github.com/dennisdevulder/ltm
cd ltm
go build -o ltm ./cmd/ltm
```

### On your server (VPS, OpenClaw instance, localhost)

```bash
# First-time setup. Prints a root token — copy it.
ltm server init --db ~/.local/share/ltm/ltm.db

# Run the server.
ltm server --addr :8080
```

### On any client machine

```bash
ltm auth http://your-vps:8080 <paste-root-token>

# Write a packet (see SPEC.md for the shape), then push it.
ltm push my-packet.json
# Or from stdin — agents can pipe directly:
cat packet.json | ltm push -

# Work with what's on the server.
ltm ls
ltm show <id>
ltm pull <id>     # raw JSON to stdout
ltm rm <id>
```

### Issue more tokens

```bash
# On the server:
ltm server issue-token laptop
ltm server issue-token ci
```

## What's in the box today

- **CLI**: `auth`, `config`, `push`, `pull`, `ls`, `show`, `rm`, `server`, `server init`, `server issue-token`.
- **Server**: single Go binary, SQLite storage, bearer-token auth, ~150 lines of HTTP handlers.
- **Validation**: JSON Schema for the Core Memory Packet, embedded in the binary.
- **Redaction pre-flight**: rejects packets containing absolute paths, AWS keys, GitHub tokens, JWTs, or private keys before they leave your machine. Override with `--allow-unredacted`.

## What's not here yet

- OAuth device flow (today: paste-a-token).
- MCP server (planned — a natural follow-up).
- Packet chaining, sharing, team spaces, federation.
- Homebrew formula and pre-built release binaries.
- A real test suite. (Smoke test works; full coverage is TODO.)

## Status

Pre-alpha. The spec is drafting — expect breaking changes before `v1.0`. Pin against the `ltm_version` field when writing packets.

## License

[Apache 2.0](./LICENSE)
