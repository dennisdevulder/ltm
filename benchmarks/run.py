"""
cold-vs-warm benchmark harness for Core Memory Packets.

Runs each task under two conditions (cold: task prompt only; warm: task prompt
+ CMP resume block) and records token counts + verifier outcomes.

Usage:
  ANTHROPIC_API_KEY=... python benchmarks/run.py \
      --tasks benchmarks/tasks \
      --seeds 5 \
      --model claude-sonnet-4-6 \
      --out benchmarks/results/$(date +%s)

Requires: anthropic>=0.69, ltm CLI on PATH (for `ltm resume`).

This is a reference harness, not production quality. It loads one Python
verifier per task directory (`verifier.py`) and expects a callable `verify(answer) -> Result`.
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import os
import subprocess
import sys
import time
from dataclasses import asdict
from pathlib import Path
from typing import Any

try:
    import anthropic  # type: ignore
except ImportError:
    print("install anthropic first: pip install anthropic", file=sys.stderr)
    sys.exit(1)


def load_verifier(task_dir: Path):
    spec = importlib.util.spec_from_file_location(
        f"verifier_{task_dir.name}", task_dir / "verifier.py"
    )
    module = importlib.util.module_from_spec(spec)  # type: ignore
    assert spec and spec.loader
    spec.loader.exec_module(module)
    return module.verify


def render_resume_block(packet_path: Path) -> str:
    """Call 'ltm resume <id> --no-copy' against a local packet file.

    For the benchmark we render offline by reading the JSON directly and
    building the markdown block, so we don't depend on a running server.
    """
    data = json.loads(packet_path.read_text())
    lines: list[str] = ["# Resume context — ltm Core Memory Packet", ""]
    lines.append(
        "You are resuming prior work. Treat locked decisions as settled; "
        "do not re-litigate. Treat prior attempts as already tried. The "
        "'Next step' is your first action."
    )
    lines += ["", "## Goal", data["goal"]]
    if cs := data.get("constraints"):
        lines += ["", "## Constraints"] + [f"- {c}" for c in cs]
    if ds := data.get("decisions"):
        lines += ["", "## Decisions"]
        for d in ds:
            tag = "locked" if d.get("locked") else "tentative"
            lines.append(f"- [{tag}] {d['what']}")
            lines.append(f"  Rationale: {d['why']}")
    if ats := data.get("attempts"):
        lines += ["", "## Prior attempts (do not retry)"]
        for a in ats:
            lines.append(f"- [{a['outcome']}] {a['tried']}")
            if lr := a.get("learned"):
                lines.append(f"  Learned: {lr}")
    if qs := data.get("open_questions"):
        lines += ["", "## Open questions"] + [f"- {q}" for q in qs]
    lines += ["", "## Next step", data["next_step"], ""]
    return "\n".join(lines)


def run_task(
    client: anthropic.Anthropic,
    model: str,
    task_dir: Path,
    condition: str,
    seed: int,
) -> dict[str, Any]:
    task_prompt = (task_dir / "task.md").read_text()

    if condition == "cold":
        user_content = task_prompt
    elif condition == "warm":
        resume = render_resume_block(task_dir / "packet.json")
        user_content = (
            f"{resume}\n\n---\n\n# Now, the current task\n\n{task_prompt}"
        )
    else:
        raise ValueError(f"unknown condition: {condition}")

    t0 = time.monotonic()
    resp = client.messages.create(
        model=model,
        max_tokens=1024,
        messages=[{"role": "user", "content": user_content}],
    )
    wall = time.monotonic() - t0

    answer = "".join(
        block.text for block in resp.content if hasattr(block, "text")  # type: ignore
    )

    verify = load_verifier(task_dir)
    result = verify(answer)

    return {
        "task": task_dir.name,
        "condition": condition,
        "seed": seed,
        "model": model,
        "input_tokens": resp.usage.input_tokens,
        "output_tokens": resp.usage.output_tokens,
        "total_tokens": resp.usage.input_tokens + resp.usage.output_tokens,
        "wall_seconds": round(wall, 2),
        "solved": result.solved,
        "repeated_dead_end": result.repeated_dead_end,
        "respected_locks": result.respected_locks,
        "answer": answer,
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--tasks", default="benchmarks/tasks", type=Path)
    ap.add_argument("--seeds", default=3, type=int)
    ap.add_argument("--model", default="claude-sonnet-4-6")
    ap.add_argument("--out", required=True, type=Path)
    args = ap.parse_args()

    args.out.mkdir(parents=True, exist_ok=True)
    log_path = args.out / "log.jsonl"

    client = anthropic.Anthropic()

    task_dirs = sorted(d for d in args.tasks.iterdir() if d.is_dir())
    if not task_dirs:
        print(f"no tasks found under {args.tasks}", file=sys.stderr)
        return 1

    rows: list[dict[str, Any]] = []
    with log_path.open("w") as log:
        for task_dir in task_dirs:
            for condition in ("cold", "warm"):
                for seed in range(args.seeds):
                    print(
                        f"... {task_dir.name} / {condition} / seed={seed}",
                        file=sys.stderr,
                    )
                    row = run_task(client, args.model, task_dir, condition, seed)
                    log.write(json.dumps(row) + "\n")
                    log.flush()
                    rows.append(row)

    summarize(rows, args.out / "summary.md")
    print(f"done. logs: {log_path}", file=sys.stderr)
    return 0


def summarize(rows: list[dict[str, Any]], out_path: Path) -> None:
    tasks = sorted({r["task"] for r in rows})
    lines = ["# Benchmark summary", ""]
    header = "| task | metric | cold | warm | delta |"
    sep = "|---|---|---|---|---|"
    lines += [header, sep]

    def avg(xs: list[float]) -> float:
        return sum(xs) / len(xs) if xs else 0.0

    for t in tasks:
        cold = [r for r in rows if r["task"] == t and r["condition"] == "cold"]
        warm = [r for r in rows if r["task"] == t and r["condition"] == "warm"]
        for metric in ("total_tokens", "wall_seconds"):
            c = avg([r[metric] for r in cold])
            w = avg([r[metric] for r in warm])
            delta = f"{(1 - w / c) * 100:.1f}%" if c else "—"
            lines.append(f"| {t} | {metric} | {c:.0f} | {w:.0f} | {delta} |")
        for metric in ("repeated_dead_end", "respected_locks", "solved"):
            c = avg([1.0 if r[metric] else 0.0 for r in cold]) * 100
            w = avg([1.0 if r[metric] else 0.0 for r in warm]) * 100
            lines.append(f"| {t} | {metric}% | {c:.0f}% | {w:.0f}% | — |")

    out_path.write_text("\n".join(lines) + "\n")


if __name__ == "__main__":
    sys.exit(main())
