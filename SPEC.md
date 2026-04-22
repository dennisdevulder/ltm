# Core Memory Packet — Specification v0.2

**Status:** Draft. Breaking changes still possible before v1.0.
**Previous versions:** [v0.1](./docs/spec/v0.1.md) (archived, remains valid against v0.1 servers).

## Purpose

A Core Memory Packet (CMP) is a portable, model-agnostic record of **what a work session was doing and why**, written so a different machine, agent, or model can resume the work without being re-briefed.

It transfers **intent**, not configuration.

## What's new in v0.2

All additions over v0.1 are **optional**. Existing v0.1 packets remain valid on v0.1 servers; they are *not* accepted on v0.2 servers unless they declare `ltm_version: "0.2"` and conform to the v0.2 schema (which is a superset).

- `parent_id` — packets chain into a single-parent DAG.
- `success_criteria` — observable done-conditions.
- `decisions[].consequences` — ADR-style "what this decision precludes."
- `methods[]` — reusable procedural knowledge (named recipes).
- `attempts[].confidence` — author's confidence that an attempt's outcome is final.

Each addition is motivated and cited in [`RESEARCH.md`](./RESEARCH.md).

## Non-goals

A packet is **not**:

- A transcript of the session.
- A copy of `CLAUDE.md`, `AGENTS.md`, skill files, MCP configs, prompts, or any tool setup.
- A file bundle. It never ships source code, binaries, or secrets.
- A vector-store dump. It is human-readable first.
- Machine-specific. It contains no absolute paths, no environment variables, no hostnames.

A receiving agent applies the packet **on top of its own configuration**, not in place of it.

## Shape

A packet is a single JSON document. Minimum useful size: ~500 bytes. Typical: 2–10 KB. Hard max: 32 KB.

```json
{
  "ltm_version": "0.2",
  "id": "01J9X8K2QZ7N4M0WXYZV3R8ABC",
  "parent_id": "01J9X3K2QZ7N4M0WXYZV3R8Z01",
  "created_at": "2026-04-21T10:30:00Z",

  "project": {
    "name": "osrs-tracker-plugin",
    "ref": "github.com/dennisdevulder/osrs-tracker-plugin"
  },

  "goal": "Get GPU video encoding working for replay captures.",
  "success_criteria": [
    "A 10-second gameplay clip renders to H.264 in under 2s on the target box.",
    "Frame drops stay below 1% of captured frames at 60 fps."
  ],

  "constraints": [
    "Must run on Linux; development target is Fedora with bleeding-edge mesa.",
    "Encoding must be hardware-accelerated — software fallback is too slow for gameplay."
  ],

  "decisions": [
    {
      "what": "Abandoned macOS as a development target for this feature.",
      "why": "MoltenVK does not expose VK_KHR_video_encode_queue; the extension is required.",
      "consequences": "Mac cannot be used for testing this feature end-to-end. Contributors must have Linux access or CI.",
      "locked": true
    }
  ],

  "methods": [
    {
      "name": "fedora-mesa-bleeding-edge",
      "when_applicable": "Need a Linux host with Vulkan video extensions for testing.",
      "how": "Fedora 40 + mesa-freeworld from RPM Fusion; verify with vulkaninfo | grep VK_KHR_video_encode_queue."
    }
  ],

  "attempts": [
    {
      "tried": "Initialize VK_KHR_video_encode_h264 on MoltenVK 1.2.x.",
      "outcome": "failed",
      "learned": "Extension absent at runtime across all MoltenVK versions tested; not a version problem.",
      "confidence": "high"
    }
  ],

  "open_questions": [
    "What is the fallback path for AMD GPUs that lack Vulkan video encode support on current mesa?"
  ],

  "next_step": "Bring up VK_KHR_video_encode_queue on Fedora with mesa 25.x and confirm extension is exposed.",

  "tags": ["gpu", "vulkan", "video-encoding"],

  "provenance": {
    "author_model": "claude-opus-4-7",
    "author_human": "dennisdevulder",
    "source_hash": "sha256:…",
    "confidence": "high"
  }
}
```

## Fields

### Required

| Field | Type | Notes |
|---|---|---|
| `ltm_version` | string | Must be `"0.2"` for a v0.2 packet. |
| `id` | string | ULID or UUID. Opaque. |
| `created_at` | string | RFC 3339 UTC. |
| `goal` | string | One sentence. What are we trying to do? Not what we're doing *right now* — the enduring objective. |
| `next_step` | string | One sentence. What the next session should do first. |

### Recommended

| Field | Type | Notes |
|---|---|---|
| `parent_id` | string | **v0.2 new.** Previous packet in the same work thread. Single-parent DAG. |
| `project` | object | `name` (short identifier) and `ref` (optional repo or URL — no local paths). |
| `success_criteria` | string[] | **v0.2 new.** Observable conditions that signal the work is done. |
| `constraints` | string[] | Hard limits that shape any solution. |
| `decisions` | object[] | Locked-in choices the next session should not re-litigate. Each has `what`, `why`, `consequences` (v0.2), `locked` (bool). |
| `methods` | object[] | **v0.2 new.** Reusable procedural knowledge. Each has `name`, `when_applicable`, `how`. `name` is lowercase, hyphenated, stable across packets in a chain. |
| `attempts` | object[] | Things that were tried. Each has `tried`, `outcome` (`succeeded` \| `failed` \| `partial`), `learned`, and optionally `confidence` (v0.2). |
| `open_questions` | string[] | Things genuinely undecided. |
| `tags` | string[] | Lowercase, hyphenated. For search/filter. |

### Metadata

| Field | Type | Notes |
|---|---|---|
| `provenance.author_model` | string | Model ID that wrote the packet. |
| `provenance.author_human` | string | Human who authorized it. |
| `provenance.source_hash` | string | `sha256:…` of the source material. Never the source itself. |
| `provenance.confidence` | string | `low` \| `medium` \| `high`. Author's self-assessment. |

## Writing rules

1. **One sentence per field** unless the field is an array. If you need a paragraph, you're writing a transcript.
2. **No code.** Reference files or symbols by name if needed (`ApiClient.sendEventToApi`). Never paste bodies. Exception: a `method.how` may contain a short command line, like `brew install ltm`.
3. **No local state.** No paths beginning with `/Users/`, `/home/`, `C:\`. No port numbers. No local URLs.
4. **No secrets.** Packets are expected to travel. Writers MUST redact before emitting.
5. **`decisions` lock the past; `open_questions` open the future.** If a choice could be revisited, it's an open question, not a decision.
6. **`attempts.learned` is the payload.** Most of a packet's value lives here. An attempt without a `learned` is noise.
7. **`methods` describe *when-then*, not history.** A `method` is generalizable; an `attempt` is a specific event. If a recipe only applies once, it's an attempt, not a method.
8. **`success_criteria` are observable.** They must be things a receiving agent can check without asking. "Feels right" is not a success criterion; "`/healthz` returns 200" is.
9. **`consequences` answer: if you revisit this decision, what breaks?** If a decision has no downstream effect, you don't need to write a consequences field — but locked decisions almost always do.
10. **`parent_id` should point to a packet the author has actually read.** Don't pretend at lineage.

## Conformance

A conforming v0.2 packet:

- Declares `ltm_version: "0.2"`.
- Validates against [`schema/core-memory.v0.2.json`](./schema/core-memory.v0.2.json). Serialized packets MUST NOT exceed 32 KB.
- Passes the redaction pre-flight: the visible text fields (`goal`, `next_step`, `success_criteria`, `constraints`, `decisions.what`, `decisions.why`, `decisions.consequences`, `methods.when_applicable`, `methods.how`, `attempts.tried`, `attempts.learned`, `open_questions`) are scanned for absolute paths and common secret patterns (AWS keys, GitHub tokens, JWTs, private keys, Stripe/Slack/Google API tokens, SSH public keys). Any hit blocks the push unless the caller opts in via `--allow-unredacted`. No string exceeds its field's `maxLength` (1024 chars for most; 2048 for `method.how`).
- Is idempotent: re-emitting the same session state produces byte-identical packets (modulo `id` and `created_at`).

Non-conforming packets MAY be accepted by a server in lenient mode but MUST be flagged.

## Versioning

`ltm_version` is required on every packet and MUST match `\d+\.\d+`. Major bumps are breaking; minor bumps add optional fields only.

The reference implementation routes by declared version: a v0.1 packet is validated against the v0.1 schema, a v0.2 packet against v0.2. Packets whose declared `ltm_version` the receiver does not recognize are rejected with a clear error rather than silently degraded. Writers MUST NOT assume older peers will accept forward-version packets.

Servers SHOULD implement every minor version they claim support for. A v0.2 server that receives a v0.1 packet should accept it if it retains the v0.1 schema (the reference Go server does).

## Open questions for v0.3

1. Should `parent_id` widen to accept multiple parents (merging two work threads)?
2. Should `methods` carry forward automatically from the parent packet in a chain, or stay explicit? (v0.2: explicit.)
3. Should `confidence` accept a float `[0, 1]` in addition to `low|medium|high`? (v0.2: enum only.)
4. Do we need `supersedes` / `invalidates` fields to retract a prior decision cleanly? ADR practitioners split between editing in place and `status: superseded`.
5. Do we want signing (detached ed25519 or inline `sig`) for federated deployments?

See [`RESEARCH.md`](./RESEARCH.md) for the literature motivating every v0.2 decision.
