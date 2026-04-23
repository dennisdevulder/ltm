# ltm

[![CI](https://github.com/dennisdevulder/ltm/actions/workflows/ci.yml/badge.svg)](https://github.com/dennisdevulder/ltm/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/dennisdevulder/ltm)](https://goreportcard.com/report/github.com/dennisdevulder/ltm)
[![License: Apache 2.0](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](./LICENSE)

Portable context for AI work sessions.

![ltm in action](demo.gif)

ltm moves the *intent and state* of a work session between machines, models, and agents. Your CLAUDE.md, skills, prompts, and tool configs stay where they are. Only the understanding of what you're doing travels: the goal, the decisions you've locked in, what you've tried, what broke, and what to do next.

## The problem it solves

You're three hours into a task on your laptop, you hit a wall (wrong OS, wrong GPU, wrong model), and you jump to a different machine. The new session has no memory of what you were doing. You spend an hour re-briefing the agent. The agent makes the same mistakes you already solved for.

Existing "AI memory" products solve this for a single agent on a single machine. None of them move cleanly between providers or hosts, and most either require enterprise contracts or bundle enough local configuration that they break on the far side.

ltm is the smallest useful thing that fixes this:

- a **protocol**, the [Core Memory Packet](./SPEC.md), that captures intent, decisions, open questions, and next steps in a few kilobytes of JSON
- a **CLI**, `ltm`, that writes, validates, pushes, and pulls packets
- a **server**, a single Go binary with SQLite storage and bearer-token or OAuth-device-flow auth, that runs on any Linux box with room for a small database

## Try it in 60 seconds, no server required

```bash
curl -fsSL https://ltm-cli.dev/install | sh
ltm example --resume
# ✓ resume block copied to clipboard. Paste into your agent session.
```

`ltm example` ships an embedded sample packet (the llama.cpp CUDA port)
so you can see what a Core Memory Packet looks like and how a fresh
agent picks up mid-thought from one, without signing up for anything
or standing up a server. When you're ready for the real thing, `ltm
auth` and you're wired up to the managed hub or your own box.

MCP-aware clients can skip the clipboard step entirely — register
`ltm mcp` once (`claude mcp add ltm -- ltm mcp`) and call ltm verbs
as tools directly from your agent.

## Install

```bash
# macOS / Linux, amd64 and arm64.
curl -fsSL https://ltm-cli.dev/install | sh
```

Or from source:

```bash
git clone https://github.com/dennisdevulder/ltm
cd ltm
go build -o ltm ./cmd/ltm
```

## Use it (client)

```bash
# Sign in. Three supported forms:
ltm auth                                  # OAuth device flow against platform.ltm-cli.dev
ltm auth https://your-vps.example         # OAuth device flow against a self-hosted server
ltm auth https://your-vps.example <token> # Paste a pre-issued bearer token

# Write a packet by hand or have an agent emit one, then push it.
ltm push packet.json
cat packet.json | ltm push -              # agents can pipe directly

# Browse what's on the server.
ltm ls
ltm show <id>
ltm pull <id>                             # raw JSON to stdout
ltm rm <id>

# Pick up where you left off.
ltm resume

# See a valid packet without needing a server at all.
ltm example

# Upgrade in place.
ltm update
```

## Wire it into your agent (MCP)

`ltm mcp` speaks the Model Context Protocol over stdio, so any MCP-aware client
can call the same verbs as tools. No second credential surface; it reuses the
host and token `ltm auth` already stored.

```bash
# Claude Code:
claude mcp add ltm -- ltm mcp

# Cursor, Zed, Claude Desktop, Continue: paste into the client's MCP config:
# { "ltm": { "command": "ltm", "args": ["mcp"] } }
```

Tools exposed: `ls`, `show`, `pull`, `resume`, `push`, `rm`, `example`, `whoami`.
Ask the agent to "resume the latest packet" or to `example` it. No id juggling.

## Run it (server)

```bash
# First-time setup. Prints a root token. Copy it; it's never shown again.
ltm server init --db ~/.local/share/ltm/ltm.db

# Run the server.
ltm server --addr :8080

# Issue more tokens for more machines.
ltm server issue-token laptop
ltm server issue-token ci
```

HTTPS is your job. Put it behind Caddy, nginx, or a reverse proxy of your choosing.

## Packets travel. Secrets don't.

A protocol whose core promise is "packets travel" has to take seriously what
travels with them. Every packet is scanned before it leaves your machine, and
any hit blocks the push unless you explicitly opt in with `--allow-unredacted`.

The pre-flight refuses to ship:

- Absolute paths on POSIX and Windows: anything under `/Users/…`, `/home/…`,
  `/opt/…`, `/var/…`, `/etc/…`, `/root/…`, or `C:\…`, so the other side
  doesn't learn the shape of your filesystem
- AWS access-key and ARN identifiers: `AKIA`, `ASIA`, `AGPA`, `AIDA`, `AROA`,
  `AIPA`, `ANPA`, `ANVA`, `ABIA`, `ACCA`
- GitHub tokens: `ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`
- JWTs (three base64url segments, header decodes to a JOSE object)
- Private-key headers (`-----BEGIN [RSA|EC|OPENSSH|] PRIVATE KEY-----`)
- Google API keys (`AIza…`, 39 chars)
- Slack tokens: `xoxa-`, `xoxb-`, `xoxp-`, `xoxr-`, `xoxs-`
- Stripe keys and webhook secrets: `sk_live_…`, `sk_test_…`, `rk_live_…`,
  `rk_test_…`, `whsec_…`
- SSH public keys (`ssh-rsa`, `ssh-ed25519`, `ssh-dss`, `ssh-ecdsa` followed
  by a base64 `AAAA…` payload)

The scanner inspects only the visible text fields the spec lists as
travelable: `goal`, `next_step`, `success_criteria[]`, `constraints[]`,
`decisions[].what` / `.why` / `.consequences`, `methods[].when_applicable`
/ `.how`, `attempts[].tried` / `.learned`, and `open_questions[]`.
Everything else in a packet is structure, not content.

This is not a theoretical posture. It's a load-bearing trust property:
packets are meant to move between machines, teams, and agents, and the
person writing the packet is not always the person reading it.

## What's in the box

- **CLI**: `auth` (and `whoami`), `config` (`set`/`get`/`unset`/`list`/`edit`/`path`), `push`, `pull`, `ls`, `show`, `rm`, `resume`, `example`, `update`, `server` (with `init` and `issue-token`), `mcp`.
- **MCP server**: `ltm mcp` is a stdio-based Model Context Protocol server that exposes the verbs above as tools to Claude Code, Cursor, Zed, Claude Desktop, Continue, or any MCP-aware client. Reuses the CLI's auth, config, schema validation, and redaction pre-flight.
- **HTTP API**: `GET /v1/healthz`, plus bearer-authed `POST/GET/DELETE /v1/packets`. Max packet size is 32 KB.
- **Packet validation**: JSON Schema for v0.1 and v0.2, embedded in the binary and routed by the declared `ltm_version`.
- **Redaction pre-flight**: see the [section above](#packets-travel-secrets-dont).
- **OAuth 2.0 device-authorization flow** (RFC 8628) against the managed hub. No token copy-paste.

## Principles

1. *Intent is portable; configuration isn't.* Packets never carry your CLAUDE.md, skills, prompts, or tool setup. Your setup is yours.
2. *Self-host or nothing.* No hosted tier unlocks features. If it doesn't run on a $5 VPS, it's not done.
3. *Model-agnostic.* A packet written by Claude is readable by GPT, Gemini, or whatever comes next. No vendor fields.
4. *Spec first, code second.* The [protocol](./SPEC.md) is the product. The CLI and server are reference implementations.
5. *Redact aggressively.* Packets are expected to travel. Secrets and local state never ride along.

## What's not here yet

- Packet sharing, team spaces, federation. Chaining exists in the v0.2 schema via `parent_id`; servers don't surface it yet.
- Windows binaries. Linux and macOS only, amd64 and arm64.
- A portable conformance test suite for second implementations. The Go reference tests stand in for one today.
- A fuzz and end-to-end harness on top of the existing unit and integration tests.

## Further reading

- [SPEC.md](./SPEC.md) — the wire protocol and packet schema. Read this if you're porting a client or a server.
- [RESEARCH.md](./RESEARCH.md) — literature review that shaped the v0.2 schema (agent memory, prompt compression, case-based reasoning, ADRs).
- [CONTRIBUTING.md](./CONTRIBUTING.md) — how to propose changes and how to port a second implementation.
- [SECURITY.md](./SECURITY.md) — reporting security issues and the redaction policy.

## Status

Pre-alpha. The spec is a draft. Expect breaking changes before `v1.0`. Pin against the `ltm_version` field when you write a packet.

## License

[Apache 2.0](./LICENSE)
