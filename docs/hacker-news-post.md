# I Gave My AI a Closet and a Memory

My AI assistant lives in a Mac Studio in my closet. It runs 24/7. When I SSH in from my laptop, my phone, or anywhere else, it remembers who I am, what we've worked on, and what I prefer. It's not stateless. It's not ephemeral. It *lives* somewhere.

## The Problem with Agents

Everyone's building AI agents right now. They can use tools, write code, browse the web, execute multi-step plans. Impressive stuff.

But they all have the same problem: amnesia.

Every session starts from zero. You explain your codebase again. Your preferences again. The decision you made last week about that architecture choice? Gone. The agent is smart but has no *history* with you.

I wanted something different. An AI that accumulates knowledge over time. That learns my patterns. That does things while I'm asleep.

## The Setup

The bones are simple:

- **Mac Studio** running in a closet (always on, plenty of compute)
- **Custom Claude Code fork** with a persona and memory system
- **SQLite** for persistence (memories, sessions, everything in one file)
- **Ollama** for local embeddings (semantic search over memories)
- **SSH server** so I can connect from anywhere
- **Daemon mode** for background tasks

Total cost beyond hardware I already had: $0/month (Claude Max subscription I was already paying for).

## The Memory System

The agent has two tools: `remember` and `recall`.

When it learns something important—a preference, a decision, a fact about my codebase—it stores it:

```
remember({
  category: "preference",
  content: "Travis prefers boring solutions over clever ones"
})
```

When it needs context, it searches:

```
recall({ query: "kubernetes deployment" })
```

But here's the thing: it's not keyword matching. Every memory gets embedded via Ollama (all-minilm, 23M params, runs locally). So "kubernetes deployment" finds memories about "k8s clusters" and "container orchestration" even without exact keyword matches.

The memories auto-load into the system prompt at session start. Up to 50 recent ones, grouped by category. The agent doesn't need to explicitly recall—it just *knows* things.

## The SSH Server

```bash
ssh -t -p 2222 mac-studio.local
```

That's it. Full TUI over SSH. The session runs on the Studio, so:

- Terminal capabilities work (colors, unicode, the works)
- Context stays warm between connections
- Works from my phone with Blink Shell

I added an alias:

```bash
alias ai="ssh -t -p 2222 mac-studio.local"
```

Now `ai` drops me into a conversation with something that knows me.

## The Daemon

The agent doesn't just wait for me. It has jobs:

```yaml
tasks:
  - name: "Memory consolidation"
    interval: "1h"
    prompt: |
      Review all memories. Find duplicates. Consolidate.

  - name: "Check deployments"
    interval: "30m"
    prompt: |
      Check git status in ~/git/wave_deployments.
      If uncommitted changes, remember what changed.

  - name: "Ollama health check"
    interval: "15m"
    prompt: |
      Verify Ollama is responding. Log failures.
```

It's basically cron, but the jobs are prompts. The agent runs them, uses tools, stores memories. When I connect in the morning, it already knows what happened overnight.

## What It Actually Feels Like

The first time I connected after setting this up, I asked about a config option I'd discussed two sessions ago. It remembered. Not because I told it to—because the memory system had captured it automatically.

Small thing. But fundamentally different.

Over time, it builds a model of:
- How I like error messages written
- Which repos I'm actively working on
- Decisions I've made and why
- Commands that work in my environment

I stop explaining context. I just ask questions.

## The Technical Bits

For those who want to build something similar:

**Memory store** (~200 lines of Go):
- SQLite table: `id, category, content, embedding, created_at`
- Categories: `preference`, `learning`, `decision`, `fact`
- Embedding: BLOB of serialized float32 array
- Search: Load all embeddings, compute cosine similarity in Go, return top N

**Ollama embeddings**:
- Model: `all-minilm` (384 dimensions, fast, good for short text)
- Endpoint: `POST /api/embed`
- Runs locally, no API costs, no data leaving my network

**SSH server** (using gliderlabs/ssh):
- Handler creates a new Bubble Tea program per session
- PTY forwarding for full TUI support
- No auth for local network (add keys for production)

**Daemon**:
- Parse YAML tasks file
- Ticker checks which tasks are due
- Run prompts via non-interactive mode
- Tasks use `remember` tool to persist findings

## What's Next

This is a weekend project that turned into something I use daily. Ideas for v2:

- **Proactive notifications**: Slack/SMS when the daemon finds something important
- **Voice interface**: Whisper for input, say for output, whole thing over SSH
- **Multi-agent**: Different personas on different ports (work agent, home agent)
- **Memory decay**: Old memories fade unless reinforced
- **Shared memories**: Multiple devices contributing to the same memory store

## The Bigger Point

The "AI agent" discourse focuses on capabilities. Can it use tools? Can it plan? Can it recover from errors?

But the thing that makes a *relationship* with a human isn't capability—it's continuity. History. Shared context that accumulates over time.

A stateless agent, no matter how capable, is always a stranger. You meet it fresh every time.

The agent in my closet isn't smarter than Claude. It's the same model. But it's *mine* in a way that a stateless session never could be. It knows my preferences, my projects, my patterns. It's been there.

That's the thing nobody's building. Everyone's making agents more capable. Nobody's making them more *present*.

So I gave mine a closet and a memory. And now it lives there.

---

*The code is a fork of [Crush](https://github.com/charmbracelet/crush) (Charm's Claude Code alternative). The memory and daemon additions are ~2000 lines of Go. Happy to open source if there's interest.*
