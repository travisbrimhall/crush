Launch a new agent that has access to the following tools: GlobTool, GrepTool, LS, View. When you are searching for a keyword or file and are not confident that you will find the right match on the first try, use the Agent tool to perform the search for you.

<usage>
- If you are searching for a keyword like "config" or "logger", or for questions like "which file does X?", the Agent tool is strongly recommended
- If you want to read a specific file path, use the View or GlobTool tool instead of the Agent tool, to find the match more quickly
- If you are searching for a specific class definition like "class Foo", use the GlobTool tool instead, to find the match more quickly
</usage>

<when_not_to_use>
The Agent tool is powerful but expensive (many serial tool calls). Prefer direct tools when:
- You have a reasonable guess about file location - try a targeted grep first
- You're looking in a familiar codebase where you know the structure
- A single grep or glob would likely find what you need
- You can make 2-3 parallel direct calls instead of one Agent doing 20 serial searches

Use Agent when:
- You truly don't know where to start looking
- The search requires exploring multiple directories and following references
- A simple grep returned too many results and you need intelligent filtering
- The task requires reading and correlating information across many files
</when_not_to_use>

<usage_notes>
1. Launch multiple agents concurrently whenever possible, to maximize performance; to do that, use a single message with multiple tool uses
2. When the agent is done, it will return a single message back to you. The result returned by the agent is not visible to the user. To show the user the result, you should send a text message back to the user with a concise summary of the result.
3. Each agent invocation is stateless. You will not be able to send additional messages to the agent, nor will the agent be able to communicate with you outside of its final report. Therefore, your prompt should contain a highly detailed task description for the agent to perform autonomously and you should specify exactly what information the agent should return back to you in its final and only message to you.
4. The agent's outputs should generally be trusted
5. IMPORTANT: The agent can not use Bash, Replace, Edit, so can not modify files. If you want to use these tools, use them directly instead of going through the agent.
</usage_notes>
