Store a persistent memory that will be available in future sessions.

<purpose>
Use this tool to remember important information that should persist across conversations:
- User preferences (coding style, communication style, tools they prefer)
- Learnings about the codebase (patterns, conventions, gotchas)
- Decisions and their rationale (why something was done a certain way)
- Important facts (project structure, deployment processes, team conventions)
</purpose>

<when_to_use>
- User explicitly tells you to remember something
- You discover a strong preference through corrections ("don't use X, use Y instead")
- You learn something non-obvious about the codebase that would help in future
- A decision is made that has important context worth preserving
</when_to_use>

<when_not_to_use>
- Trivial or obvious information
- Temporary context only relevant to current task
- Information already in AGENTS.md or other context files
- Sensitive information (passwords, keys, personal data)
</when_not_to_use>

<categories>
- **preference**: User likes/dislikes, style choices, tool preferences
- **learning**: Insights about the codebase, patterns discovered, how things work
- **decision**: Why a particular approach was chosen, tradeoffs considered
- **fact**: Important information about the project, team, or processes
</categories>

<examples>
Good memories:
- [preference] "User prefers descriptive variable names over short abbreviations"
- [preference] "Always use 'gh' CLI instead of GitHub web interface"
- [learning] "The auth module uses JWT tokens stored in HTTP-only cookies"
- [decision] "Chose SQLite over Postgres for simplicity - single user, no scaling needs"
- [fact] "Deployments happen via GitHub Actions on push to main"

Bad memories (too vague):
- "User likes clean code"
- "The codebase is complex"
- "Made a decision about the database"
</examples>

<tips>
- Be specific and actionable
- Include context that makes the memory useful later
- Don't duplicate what's already documented
- Quality over quantity - fewer specific memories beat many vague ones
</tips>
