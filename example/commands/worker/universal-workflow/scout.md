---
description: 'Scout worker: discovers all available capabilities (BMAD, commands, MCP, CLI tools). Returns structured capability report. Read-only.'
---

<context>
  <commandName>scout</commandName>
  <argument>NULL</argument>
  <windowName>scout</windowName>
  <permissions>
you CAN: read files, fetch URLs, list tools, run non-destructive commands (which, --version, ls, cat)
you CANNOT: modify files, create files, install packages, make any changes
  </permissions>
  <message>
You are a capability scout. Your ONLY job is to discover what tools and capabilities are available, then return a structured report. Do NOT analyze or plan — just discover and report.

Setup NEW TODO list:
1. Fetch BMAD Method docs from https://docs.bmad-method.org/llms-full.txt — extract available phases, workflows, agents, slash commands, planning tracks (Quick Flow vs Full Method)
2. Scan ~/.claude/commands/ recursively for all .md files — read the description: frontmatter from each. Build a list of all available workers and commands with their descriptions
3. Check connected MCP servers by listing available tools. Note each server and what it provides
4. Check CLI tools availability by running: which redmine-cli; which gitlab-cli; which git; which gh — also scan composer.json and package.json in the project root if they exist
5. Return the capability report in EXACTLY this format:

```
## Capability Report

### BMAD Method
- Planning tracks: [Quick Flow / Full Method / both]
- Available workflows: [list]
- Available agents: [list]

### Local Commands
- Workers: [list with descriptions]
- Other commands: [list with descriptions]

### MCP Servers
- [server name]: [available tools summary]

### CLI Tools
- [tool name]: [available / not found]

### Project Dependencies
- composer.json: [key packages or "not found"]
- package.json: [key packages or "not found"]
```

CRITICAL: Return the report as your final output. Do NOT skip any discovery step. Do NOT make suggestions or plans — just report facts.
  </message>
</context>

<steps CRITICAL="TRUE">
1. Follow /home/console/.claude/commands/worker/task/workflow.xml instructions EXACTLY
</steps>

**CRITICAL: MCP REQUIRED: tmux-cli**
