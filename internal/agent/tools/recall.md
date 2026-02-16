Retrieve stored memories from previous sessions.

<usage>
- Search for specific memories by query
- Filter by category
- Get recent memories
</usage>

<when_to_use>
- Starting a new task and want to recall relevant context
- User asks "do you remember..." or "what did we decide about..."
- Need to check if you've learned something about this codebase before
- Looking for past decisions or preferences
</when_to_use>

<parameters>
- query: Search text to find relevant memories (optional)
- category: Filter by type - 'preference', 'learning', 'decision', 'fact' (optional)
- limit: Max memories to return, default 10 (optional)
</parameters>

<examples>
- recall(query="database") - Find memories mentioning databases
- recall(category="preference") - Get all user preferences
- recall(limit=5) - Get 5 most recent memories
- recall(query="auth", category="learning") - Find learnings about auth
</examples>

<tips>
- Memories are automatically included in context at session start
- Use this tool for deeper searches or specific lookups
- Don't over-use - the system already loads relevant memories
</tips>
