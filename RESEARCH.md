# Research notes — toward Core Memory Packet v0.2

**Status:** draft. A living document on the `research/protocol-v0.2` branch.
**Question:** what does existing literature say about compressing the intent and state of a work session so a future agent can resume without re-briefing? How should that reshape the Core Memory Packet schema?

This file distills published and practitioner work across four adjacent traditions — **agent memory systems**, **prompt compression**, **case-based reasoning**, and **architectural decision records** — and proposes concrete v0.2 refinements backed by that literature.

---

## 1. Four bodies of work worth knowing

### 1.1 Memory systems for LLM agents

| System | Year | What it contributes |
|---|---|---|
| **MemGPT / Letta** ([Packer et al. 2023](https://arxiv.org/abs/2310.08560)) | 2023 | Two-tier memory: *in-context core memory* (always present) + *external recall/archival* (paged in on demand). LLM edits its own memory via tools. Recursive summarization for eviction. Establishes the OS analogy. |
| **Generative Agents** ([Park et al. 2023](https://arxiv.org/abs/2304.03442)) | 2023 | *Memory stream* of raw observations → *reflections* (higher-level syntheses) → retrieval by recency × importance × relevance. Reflections are themselves memories. |
| **Reflexion** ([Shinn et al. 2023](https://arxiv.org/abs/2303.11366)) | 2023 | *Verbal reinforcement*: agent writes natural-language reflections on failed attempts and stores them in an episodic buffer. **Reflection-guided refinement beats refinement alone by +8 points absolute on programming tasks.** 91% pass@1 on HumanEval vs. 80% for GPT-4 baseline. |
| **Voyager** ([Wang et al. 2023](https://arxiv.org/abs/2305.16291)) | 2023 | *Ever-growing skill library* of **executable code**, not descriptions. Agent produces skills that compound. 3.3× more unique items, 15.3× faster milestone unlocks than prior SOTA in Minecraft. Skills generalize to new worlds. |
| **MIRIX-style agents** ([survey 2025](https://arxiv.org/html/2602.19320v1)) | 2024–2025 | Explicit multi-module memory: **Core / Episodic / Semantic / Procedural / Resource / Knowledge Vault**, each with type-specific fields and access policies. |
| **LongMemEval** ([Wu et al. 2024](https://arxiv.org/abs/2410.10813)) | 2024 | Five memory abilities: information extraction, multi-session reasoning, temporal reasoning, knowledge updates, abstention. Commercial assistants + long-context LLMs show ~30% accuracy drop across sustained interactions. |

### 1.2 Prompt compression

| System | Year | What it contributes |
|---|---|---|
| **LLMLingua** ([Jiang et al. 2023](https://arxiv.org/abs/2310.05736)) | 2023 | Budget Controller + token-level compression + alignment. Up to **20× compression with 1.5-point performance drop**. Preserves chain-of-thought reasoning steps even at aggressive ratios. |
| **Lost in the Middle** ([Liu et al. 2023 / TACL 2024](https://arxiv.org/abs/2307.03172)) | 2023 | Models attend best to the **start and end** of context; performance degrades for information in the middle, even in long-context models. Position matters as much as content. |

### 1.3 Case-based reasoning (CBR) — the pre-LLM tradition

| Work | Year | What it contributes |
|---|---|---|
| **Aamodt & Plaza, "Foundational Issues"** ([1994](https://journals.sagepub.com/doi/abs/10.3233/AIC-1994-7104)) | 1994 | The canonical **4R cycle: Retrieve → Reuse → Revise → Retain.** 30+ years of structured "learn from past problems" literature. |
| **CBR in software engineering** ([multiple authors](https://www.researchgate.net/publication/3586053_Case-based_reasoning_in_software_engineering)) | 1990s–2000s | Two main use-cases: *prediction* (effort, duration, risk) and *reuse* (learning from past incidents). Software knowledge reuse models map near-1:1 onto CBR. |
| **CBR for LLM agents** ([survey 2025](https://arxiv.org/html/2504.06943)) | 2025 | Bridges CBR theory to modern agents. Argues CBR addresses hallucination + contextual memory gaps. |

### 1.4 Architectural Decision Records (ADRs) — a lightweight practitioner standard

| Work | Year | What it contributes |
|---|---|---|
| **Nygard, "Documenting Architecture Decisions"** ([2011](https://www.cognitect.com/blog/2011/11/15/documenting-architecture-decisions)) | 2011 | The template that won: **Title · Status · Context · Decision · Consequences.** Short (pages, not chapters). Markdown. In the repo. 15 years of industry use. |
| **Martin Fowler's bliki** ([summary](https://martinfowler.com/bliki/ArchitectureDecisionRecord.html)) | ongoing | Reiterates: "lightweight document with a focus on the decision itself." |

---

## 2. Taxonomy — what actually belongs in portable memory

Across all four traditions, the same five categories recur:

| Category | Cognitive analogue | What it is | Maps to v0.1 field |
|---|---|---|---|
| **Core / identity** | Working + semantic core | The always-true frame: what we're doing, hard constraints we can't violate | `goal`, `constraints` |
| **Episodic** | Episodic memory | Time-ordered record of what happened, what was tried, what the outcome was | `attempts` |
| **Reflective / decisional** | Semantic (decontextualized) | Distilled judgements crystallized from episodes: decisions we've locked, rules we've internalized | `decisions` |
| **Procedural** | Procedural memory | Reusable *how-to* — recipes that worked, patterns to apply | *(missing)* |
| **Forward pointer** | Working memory | What to do next, what we're still uncertain about | `next_step`, `open_questions` |

v0.1 covers four of the five. **The gap is procedural memory.** Voyager's core insight — that *executable how* deserves its own layer distinct from *what happened* — is not reflected in the v0.1 packet.

---

## 3. Implications for the Core Memory Packet

Seven specific claims, each grounded in the literature above.

### 3.1 A packet is a case, not a transcript

CBR (Aamodt & Plaza) treats each resolved problem as a **case** with four parts: *problem description, solution, outcome, learned lessons*. The v0.1 packet is already close to this shape — `goal` + `constraints` are problem description; `decisions` + `next_step` are solution direction; `attempts` are outcomes; `open_questions` are unfinished lessons. Naming this lineage in the spec is worth doing; it signals we're building on validated theory, not inventing from scratch.

### 3.2 `decisions` should follow the ADR template more closely

Nygard's proven structure is **Context → Decision → Consequences**. v0.1 has `what` and `why` and `locked`, but no explicit *consequences* field. Without consequences, a receiving agent can't assess "if I'm tempted to revisit this decision, what would I break?" — which is precisely the re-litigation the `locked` flag is meant to prevent. **Proposal**: add optional `consequences` to each decision entry.

### 3.3 A `methods` / `skills` field for procedural memory

Voyager showed that *executable* procedures compound: "to kill a zombie, execute this code"; "to smelt iron, sequence these actions." For software agents, the equivalent is **recipes that worked** — "to bypass MoltenVK's missing `VK_KHR_video_encode_queue`, pass `--with-cuda` to the ffmpeg build." These are not decisions (they're not organizing principles) and they're not attempts (they're not time-anchored). **Proposal**: add optional `methods[]` with `{name, when_applicable, how}`.

### 3.4 Attempts deserve a `confidence` field

Reflexion's contribution is showing that **labeled reflection beats raw episodic log**. An attempt with "I'm 95% certain this approach is dead" is worth 10× an attempt with no confidence. **Proposal**: add optional `confidence: low | medium | high` on each attempt, distinct from the packet-level `provenance.confidence`.

### 3.5 Position the most authoritative info at start and end

Liu et al.'s "lost in the middle" result is load-bearing for how we *render* a packet to an LLM. Receiving agents consume the `ltm resume` output via their context window; information in the middle gets attenuated. **Proposal**: rewrite the resume block to front-load `goal` + `next_step` + locked `decisions`, put `attempts` in the middle (where they're less position-sensitive), and repeat `next_step` at the end as a prime.

### 3.6 Explicit `success_criteria` reduces token waste

SWE-bench evaluations found that **vague issue descriptions cause agents to churn and waste tokens**; a clear todo/success list reduces wasted token count while improving resolution rates. v0.1's `next_step` is a single sentence — possibly too terse to anchor. **Proposal**: add optional `success_criteria: string[]` — a short bulleted list of observable conditions that signal "you're done."

### 3.7 Packets should chain

Generative Agents' memory stream works because reflections reference prior observations; MemGPT's recall store works because paged-in memories reference prior context. v0.1's open question #1 is "should packets chain?" — the literature's answer is **yes**, because procedural and semantic memory only compound when lineage is explicit. **Proposal**: add optional `parent_id: string` → previous packet in the same work thread, and mandate that receiving agents treat `decisions` from the parent chain as inherited unless explicitly overridden.

---

## 4. Proposed v0.2 schema (diff from v0.1)

```diff
 {
   "ltm_version": "0.2",
   "id": "...",
+  "parent_id": "...",        // OPTIONAL: previous packet in this work thread
   "created_at": "...",
   "project": { "name": "...", "ref": "..." },
   "goal": "...",
+  "success_criteria": [ "..." ],   // OPTIONAL: observable done-conditions
   "constraints": [ "..." ],
   "decisions": [
     {
       "what": "...",
       "why": "...",
       "locked": true,
+      "consequences": "..."    // OPTIONAL: what this decision precludes
     }
   ],
+  "methods": [                 // OPTIONAL: reusable procedural knowledge
+    {
+      "name": "...",           //  short handle
+      "when_applicable": "...", //  one sentence trigger condition
+      "how": "..."             //  one-paragraph recipe; no code bodies
+    }
+  ],
   "attempts": [
     {
       "tried": "...",
       "outcome": "succeeded|failed|partial",
       "learned": "...",
+      "confidence": "low|medium|high"   // OPTIONAL
     }
   ],
   "open_questions": [ "..." ],
   "next_step": "...",
   "tags": [ "..." ],
   "provenance": {
     "author_model": "...",
     "author_human": "...",
     "source_hash": "sha256:...",
     "confidence": "low|medium|high"
   }
 }
```

All additions are **optional**; existing v0.1 packets remain valid under v0.2 with `ltm_version` bumped. Size cap stays at 32 KB.

---

## 5. What we're *not* changing (and why)

- **No vector embeddings** inside the packet. Mem0, LongMemEval, and Supermemory all move toward embedding-indexed retrieval — but that's server-side concern, not packet-format concern. Packets stay human-readable JSON.
- **No signing in v0.2.** Originally flagged as a v1.0 open question; the research doesn't give us a forcing function, so defer.
- **No compression codec (LLMLingua etc.).** Packets are small (≤32 KB) and hand-written or LLM-written; auto-compression adds fragility. If a packet gets too large, it's signal to split it, not compress it.
- **No `reflection` field separate from `attempts.learned`.** Reflexion treats reflection as a distinct memory type, but in practice the `learned` substring of each attempt is where reflection already lives. Splitting them would duplicate.

---

## 6. Open questions for v0.2

1. Should `parent_id` be a single-parent DAG or allow multiple parents (merging two work threads)?
2. If `methods` accumulate across a packet chain, do they carry forward automatically or must each packet re-declare the ones it's using?
3. Should `confidence` accept a float `[0, 1]` instead of low/medium/high? The lit is split — Reflexion uses qualitative; OpenAI tool-use encourages calibrated probabilities.
4. Do we need a `supersedes` / `invalidates` field to retract a prior decision cleanly? ADR practitioners split between editing in place and status="superseded".

---

## 7. References

- Aamodt, A., & Plaza, E. (1994). [Case-Based Reasoning: Foundational Issues, Methodological Variations, and System Approaches](https://journals.sagepub.com/doi/abs/10.3233/AIC-1994-7104). *AI Communications*.
- Jiang, H., et al. (2023). [LLMLingua: Compressing Prompts for Accelerated Inference of Large Language Models](https://arxiv.org/abs/2310.05736). *EMNLP*.
- Liu, N. F., et al. (2023). [Lost in the Middle: How Language Models Use Long Contexts](https://arxiv.org/abs/2307.03172). *TACL 2024*.
- Nygard, M. (2011). [Documenting Architecture Decisions](https://www.cognitect.com/blog/2011/11/15/documenting-architecture-decisions).
- Packer, C., et al. (2023). [MemGPT: Towards LLMs as Operating Systems](https://arxiv.org/abs/2310.08560).
- Park, J. S., et al. (2023). [Generative Agents: Interactive Simulacra of Human Behavior](https://arxiv.org/abs/2304.03442). *UIST*.
- Shinn, N., et al. (2023). [Reflexion: Language Agents with Verbal Reinforcement Learning](https://arxiv.org/abs/2303.11366). *NeurIPS*.
- Wang, G., et al. (2023). [Voyager: An Open-Ended Embodied Agent with Large Language Models](https://arxiv.org/abs/2305.16291).
- Wu, D., et al. (2024). [LongMemEval: Benchmarking Chat Assistants on Long-Term Interactive Memory](https://arxiv.org/abs/2410.10813). *ICLR 2025*.
- Fowler, M. (n.d.). [Architecture Decision Record](https://martinfowler.com/bliki/ArchitectureDecisionRecord.html).
- [Survey: Memory in the Age of AI Agents](https://github.com/Shichun-Liu/Agent-Memory-Paper-List).
- [Survey: Case-Based Reasoning for LLM Agents](https://arxiv.org/html/2504.06943) (2025).
