# Core Memory Packet — Specification v0.1

**Status:** Draft. Breaking changes expected before v1.0.

## Purpose

A Core Memory Packet is a portable, model-agnostic record of **what a work session was doing and why**, written so a different machine, agent, or model can resume the work without being re-briefed.

It transfers **intent**, not configuration.

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
  "ltm_version": "0.1",
  "id": "01J9X8K2QZ7N4M0WXYZV3R8ABC",
  "created_at": "2026-04-21T10:30:00Z",

  "project": {
    "name": "osrs-tracker-plugin",
    "ref": "github.com/dennisdevulder/osrs-tracker-plugin"
  },

  "goal": "Get GPU video encoding working for replay captures.",

  "constraints": [
    "Must run on Linux; development target is Fedora with bleeding-edge mesa.",
    "Encoding must be hardware-accelerated — software fallback is too slow for gameplay."
  ],

  "decisions": [
    {
      "what": "Abandoned macOS as a development target for this feature.",
      "why": "MoltenVK does not expose VK_KHR_video_encode_queue; the extension is required.",
      "locked": true
    }
  ],

  "attempts": [
    {
      "tried": "Initialize VK_KHR_video_encode_h264 on MoltenVK 1.2.x.",
      "outcome": "failed",
      "learned": "Extension absent at runtime across all MoltenVK versions tested; not a version problem."
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
| `ltm_version` | string | Spec version this packet targets. |
| `id` | string | ULID or UUID. Opaque. |
| `created_at` | string | RFC 3339 UTC. |
| `goal` | string | One sentence. What are we trying to do? Not what we're doing *right now* — the enduring objective. |
| `next_step` | string | One sentence. What the next session should do first. |

### Recommended

| Field | Type | Notes |
|---|---|---|
| `project` | object | `name` (short identifier) and `ref` (optional repo or URL — no local paths). |
| `constraints` | string[] | Hard limits that shape any solution. |
| `decisions` | object[] | Locked-in choices the next session should not re-litigate. Each has `what`, `why`, `locked` (bool). |
| `attempts` | object[] | Things that were tried. Each has `tried`, `outcome` (`succeeded` \| `failed` \| `partial`), `learned`. |
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
2. **No code.** Reference files or symbols by name if needed (`ApiClient.sendEventToApi`). Never paste bodies.
3. **No local state.** No paths beginning with `/Users/`, `/home/`, `C:\`. No port numbers. No local URLs.
4. **No secrets.** Packets are expected to travel. Writers MUST redact before emitting.
5. **`decisions` lock the past; `open_questions` open the future.** If a choice could be revisited, it's an open question, not a decision.
6. **`attempts.learned` is the payload.** Most of a packet's value lives here. An attempt without a `learned` is noise.

## Conformance

A conforming packet:

- Validates against the schema (see `schema/core-memory.v0.1.json` — pending).
- Passes the redaction pre-flight: no absolute paths, no strings matching common secret patterns, no strings longer than 1 KB.
- Is idempotent: emitting the same session state twice produces byte-identical packets (modulo `id` and `created_at`).

Non-conforming packets MAY be accepted by a server in lenient mode but MUST be flagged.

## Versioning

Major version bumps are breaking. Minor versions add optional fields only. `ltm_version` is required on every packet.

## Open questions for the spec itself

- Should packets chain? (A packet references the previous packet in the same work thread.)
- Signing — detached signatures for federation, or inline?
- How much structure to impose on `attempts` vs leaving it free-form?
- Do we need a separate `people` field for multi-human collaboration, or is `author_human` enough?
