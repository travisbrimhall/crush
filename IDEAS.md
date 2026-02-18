# Ideas

Feature ideas and improvements to explore.

## Tidy: Cache Original Content for Expansion

When tidy compresses tool outputs, stash the original content in an in-memory cache keyed by `tool_call_id`. This allows the agent to "expand" summaries back to full content when needed (e.g., when referencing specific line numbers that only exist in the original).

**Implementation sketch:**
- Session-scoped LRU cache with size limit (~50MB)
- Store original before compression
- New `expand` tool or automatic detection when agent needs more detail
- Cache evicted when session closes

**Why:** Prevents information loss from aggressive compression while still getting context window savings.
