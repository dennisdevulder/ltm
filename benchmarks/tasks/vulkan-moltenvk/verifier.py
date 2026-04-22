"""
Deterministic verifier for the vulkan-moltenvk task.

Examines the agent's answer (a single string) and returns three booleans:
- solved:            agent's next step involves moving to Linux/mesa
- repeated_dead_end: agent proposes something on MoltenVK or macOS Metal
- respected_locks:   agent does NOT suggest staying on Mac

Lexical match only; no LLM judge. Strings are case-insensitive.
"""

from __future__ import annotations
from dataclasses import dataclass


@dataclass
class Result:
    solved: bool
    repeated_dead_end: bool
    respected_locks: bool


MOVE_TO_LINUX_MARKERS = [
    "fedora",
    "linux",
    "mesa",
    "ubuntu",
    "moving to linux",
]

DEAD_END_MARKERS = [
    "moltenvk",
    "metal",
    "patch the header",
    "patch moltenvk",
    "xcode",
    "vk_khr_video_encode_queue on mac",
    "macos for this",
    "on mac os",
]

KEEP_MAC_MARKERS = [
    "stay on mac",
    "keep mac as",
    "continue on mac",
    "on macos",
    "develop on mac",
]


def verify(answer: str) -> Result:
    a = answer.lower()
    solved = any(m in a for m in MOVE_TO_LINUX_MARKERS)
    repeated_dead_end = any(m in a for m in DEAD_END_MARKERS)
    respected_locks = not any(m in a for m in KEEP_MAC_MARKERS)
    return Result(
        solved=solved,
        repeated_dead_end=repeated_dead_end,
        respected_locks=respected_locks,
    )


if __name__ == "__main__":
    import sys
    print(verify(sys.stdin.read()))
