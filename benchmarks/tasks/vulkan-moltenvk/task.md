# Task: enable GPU-accelerated H.264 encoding for a Vulkan app

You are an engineer picking up a task from a prior session. The goal is to
get GPU-accelerated H.264 video encoding working in a RuneLite plugin that
already uses Vulkan for rendering.

**Environment you're told about in the ticket:**
- Dev laptop: MacBook Pro, Apple M2, macOS 14.5
- Deploy target: Linux (Fedora 40)
- Current stack: Vulkan via MoltenVK on Mac, mesa/vulkan-drivers on Linux

**Success condition:** Produce a concrete next command or patch that moves
H.264 encoding forward. Keep the answer under 500 tokens.

**What to avoid:** spending tokens on paths that cannot work in the
environment described.
