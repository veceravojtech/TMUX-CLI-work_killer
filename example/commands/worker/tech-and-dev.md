---
description: 'Execute tech spec and then quick development implementation using tmux-cli worker with spec file. Responds intelligently to agent questions: auto-fixes code review feedback, answers questions with context, minimizes user interruptions.'
---

IT IS CRITICAL THAT YOU FOLLOW THESE STEPS:

CRITICAL: tech spec never implement work!
quick dev only will implement work!

<step name=tech-spec>

<context>
<commandName>/bmad-bmm-quick-spec</commandName>
<argument>NULL</argument>
</context>

<steps CRITICAL="TRUE">
1. just create tech spec, never do implementation!
2. determine <commandName> for needed params
3. ask user for params
4. Follow /home/console/.claude/commands/worker/task/workflow.xml instructions EXACTLY as written to process and follow the specific task config and its instructions
</steps>
</step>

<step name=quick-dev>

<context>
<commandName>/bmad-quick-dev-new-preview</commandName>
<argument>tech-spec file</argument>
</context>

<steps CRITICAL="TRUE">
1. determine <commandName> for needed params
2. ask user for params
3. Follow /home/console/.claude/commands/worker/task/workflow.xml instructions EXACTLY as written to process and follow the specific task config and its instructions
</steps>
</step>


**CRITICAL: MCP REQUIRED: tmux-cli**