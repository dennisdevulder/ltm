# ltm

[![CI](https://github.com/dennisdevulder/ltm/actions/workflows/ci.yml/badge.svg)](https://github.com/dennisdevulder/ltm/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/dennisdevulder/ltm)](https://goreportcard.com/report/github.com/dennisdevulder/ltm)
[![License: Apache 2.0](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](./LICENSE)

Git captures what a project is. `ltm` captures what it ran into: the dead ends, the arguments you had with the model and lost, the constraints that shaped the current code without ever appearing in it.

![ltm in action](demo.gif)

That second layer is the part agents can't reconstruct from a repo. A fresh session on a different harness, a different machine, or just Monday morning starts from the diff and re-learns the rest by making the same mistakes. `ltm` is the smallest useful thing that stops that: a small JSON protocol (the [Core Memory Packet](./SPEC.md)) plus a CLI and server to move packets between sessions.

A packet is a short dossier on one obstacle. Goal, decisions you've locked in, what you've already tried, what the next step is. Five required fields. Typical size: 2 to 5 KB. Forward-compatible. The 90% of work that went smoothly never needs a packet, because the commit log already carries that.

## The common path

```bash
# End of a session, agent emits a packet, redaction-checked, pushed.
ltm save

# Start of the next session, on any machine, in any harness.
ltm resume
# ✓ resume block copied to clipboard. Paste into your agent session.
```

MCP-aware agents call `save` and `resume` as tools directly; see [Wire it into your agent](#wire-it-into-your-agent-mcp) below.

## Install

```bash
# macOS, Linux. amd64 and arm64.
curl -fsSL https://ltm-cli.dev/install | sh
```

Or from a checkout: `go build -o ltm ./cmd/ltm`.

## See what a resume looks like

No server, no account, no auth. One command runs the whole flow against an embedded sample packet and drops a resume block on your clipboard.

```bash
ltm example --resume
```

This is the same flow the demo above is showing. It's the fastest way to decide whether `ltm` is worth the next five minutes.

## Use it

```bash
# Sign in. Three supported forms.
ltm auth                                         # managed hub (OAuth device flow)
ltm auth https://your-server.example             # self-hosted, if the server speaks RFC 8628 device flow
ltm auth https://your-server.example <token>     # paste a pre-issued bearer token (what the reference ltm server wants)

# Daily driver.
ltm save                                   # session to packet to push, in one step
ltm resume                                 # interactive picker, copies to clipboard
ltm resume <id>                            # skip the picker, print to stdout

# The usual CRUD when you need it.
ltm ls
ltm show <id>
ltm pull <id>
ltm rm <id>

# Handy.
ltm example                                # print a valid packet, no server required
ltm update                                 # upgrade in place
```

## Wire it into your agent (MCP)

`ltm mcp` speaks the Model Context Protocol over stdio. It exposes the client verbs (`save`, `resume`, `ls`, `show`, `pull`, `push`, `rm`, `example`, `whoami`) as tools, and it reuses whatever `ltm auth` already stored. No second credential surface.

```bash
# Claude Code.
claude mcp add ltm -- ltm mcp

# Cursor, Zed, Claude Desktop, Continue. Paste into the client's MCP config:
# { "ltm": { "command": "ltm", "args": ["mcp"] } }
```

Once registered, the agent saves at the end of a session and resumes at the start of the next. You never type an ID.

## Run your own server

One Go binary, SQLite on disk, bearer-token auth. HTTPS is your job: Caddy, nginx, a reverse proxy of your choosing.

```bash
ltm server init --db ~/.local/share/ltm/ltm.db   # prints the root token, once
ltm server --addr :8080
ltm server issue-token laptop                    # name one token per machine (laptop, ci, ...)
```

The reference server is bearer-token only. It does not implement OAuth device flow (RFC 8628) today, so clients pointed at it should use `ltm auth <host> <token>`. The managed hub implements device flow through Doorkeeper; a second implementation of the ltm protocol is free to do the same, and `ltm auth <host>` will then work against it.

## Packets travel. Secrets don't.

The core promise is that packets move between machines, teams, and agents, which means what travels with them has to be something you actually meant to send. Every packet is scanned before it leaves your machine. Any hit blocks the push unless you opt in with `--allow-unredacted`.

The pre-flight refuses absolute paths (POSIX and Windows), AWS access keys and ARNs, GitHub tokens, JWTs, private-key headers, Google API keys, Slack tokens, Stripe keys and webhook secrets, and SSH public keys. It inspects only the spec's travelable text fields (`goal`, `next_step`, `constraints`, `decisions.*`, `methods.*`, `attempts.*`, `open_questions`). Structure carries no content; content is where the leaks are.

This is load-bearing, not cosmetic. The person writing the packet is not always the person reading it. Full pattern list and rationale in [SPEC.md](./SPEC.md#conformance).

## Principles

1. Intent is portable; configuration isn't. Packets never carry your CLAUDE.md, skills, prompts, or tool setup.
2. Self-host or nothing. If it doesn't run on a $5 VPS, it's not done.
3. Model-agnostic. A packet written by Claude is readable by GPT, Gemini, or whatever comes next.
4. Spec first, code second. The [protocol](./SPEC.md) is the product; the CLI and server are reference implementations.
5. Redact aggressively. Secrets and local state never ride along.

## What's not here yet

Packet sharing, team spaces, federation. Windows binaries (Linux and macOS only, amd64 and arm64). A portable conformance suite for second implementations; the Go reference tests stand in for one today. A fuzz and end-to-end harness on top of the existing unit and integration tests. Chaining is defined in the v0.2 schema (`parent_id`) but the server doesn't surface it yet.

## How this is built

ltm is written with LLM assistance, and says so out loud. A human drives the design, writes the prose, reviews every line, and is accountable for what lands; a coding agent helps with implementation. Commits touched by an agent carry an `Assisted-by:` trailer naming the tool — the same convention as the [Linux kernel's AI Coding Assistants policy](https://docs.kernel.org/process/coding-assistants.html). Disclosure, not disguise.

If you send a PR that an LLM helped write, do the same: add an `Assisted-by:` trailer, read the diff as if you'd written it yourself, and own it. Details in [CONTRIBUTING.md](./CONTRIBUTING.md#llm-assisted-contributions).

## Further reading

[SPEC.md](./SPEC.md) for the wire format and packet schema. [RESEARCH.md](./RESEARCH.md) for the literature review that shaped v0.2 (agent memory, prompt compression, case-based reasoning, ADRs). [CONTRIBUTING.md](./CONTRIBUTING.md) for how to propose changes and how to port a second implementation. [SECURITY.md](./SECURITY.md) for reporting issues.

## Status

Pre-alpha. The spec is a draft; breaking changes are on the table before `v1.0`. Pin against `ltm_version` when you write a packet.

## License

[Apache 2.0](./LICENSE)
