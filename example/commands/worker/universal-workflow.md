---
description: 'Universal workflow builder. Discovers all available capabilities (BMAD methods, commands, MCP servers, CLI tools), builds a task-appropriate workflow, gets approval, then executes via tmux agent.'
---

## Universal Workflow — Dynamic Delegated Workers

Pipeline-style orchestrator that dynamically decides what workers to spawn based on the task. Does NOT have hardcoded stages — evaluates the task and builds the chain at runtime.

**CRITICAL: Execute stages ONE BY ONE. Wait for each stage to fully complete before starting the next.**

IT IS CRITICAL THAT YOU FOLLOW THESE STEPS:

---

### Stage 1: Run Scout

Always runs first. Invoke the scout worker to discover all available capabilities:

```
/worker:universal-workflow:scout $ARGUMENTS
```

Wait for the scout to complete and collect the **Capability Report**.

---

### Stage 2: Evaluate Task and Build Chain

Analyze the user's task (`$ARGUMENTS`) against the scout's capability report. Determine what work is needed and build an ordered chain of stages.

**Decision rules** — check each condition and add matching stages to the chain:

| Condition | Stage to add | How |
|-----------|-------------|-----|
| Task needs investigation (gather info, understand context, research issue) | Detective | Invoke `/worker:detective` |
| Task needs a technical specification | Tech Spec | Invoke `/worker:tech-spec` |
| Task needs code implementation | Quick Dev | Invoke `/worker:quick-dev` |
| Task needs browser testing | Chrome | Invoke `/worker:chrome` |
| Task needs something not covered by existing workers | Custom Worker | Compose a `<context>` block with `<message>` + `<windowName>` + `<permissions>` and execute workflow.xml directly |
| Task maps to exactly ONE existing worker | Single Worker | Just invoke that one worker — no chain needed |

**Composing custom workers** — when no existing worker fits a stage:

1. Choose a descriptive `<windowName>` (e.g., `research`, `analysis`, `migration`)
2. Write a focused `<message>` with a TODO list — ONE concern per worker
3. Set appropriate `<permissions>` (read-only for research, full for implementation)
4. Execute via workflow.xml just like the static workers do

**Chain ordering** — stages should follow a logical sequence:
- Investigation/research stages come first
- Specification stages come after investigation
- Implementation stages come after specification
- Testing/validation stages come last

---

### Stage 3: Execute the Chain

**Do NOT ask the user for approval — proceed immediately.**

Execute each stage ONE BY ONE, passing results forward:

1. **Create TODO list** with all planned stages at the start
2. **For each stage in the chain:**
   - For existing workers: invoke `/worker:*` with the appropriate arguments (include context from previous stages)
   - For custom workers: compose the `<context>` block and follow workflow.xml
   - Wait for completion before starting the next stage
   - Collect results to pass forward
3. **If a stage fails:** STOP immediately. Report which stage failed and why. Ask user how to proceed.

**Passing context forward:**
- Each stage's output becomes input context for the next stage
- Include file paths, summaries, and key findings from previous stages as arguments
- Be EXHAUSTIVE — the next worker needs all relevant context

---

### Stage 4: Final Summary

After all stages complete, print a results table:

```
## Workflow Complete: "<task description>"

| Stage | Worker | Window | Status | Output |
|-------|--------|--------|--------|--------|
| 1 | scout | scout | ... | capability report |
| 2 | ... | ... | ... | ... |
| ... | ... | ... | ... | ... |

Total stages: N
```

---

### Examples

**"investigate task 12345"** → scout → detective
**"create spec and implement booking feature"** → scout → detective → tech-spec → quick-dev
**"test checkout in browser"** → scout → chrome
**"research BMAD architecture patterns"** → scout → custom research worker (read-only, BMAD-focused message)
**"full pipeline for task 12345"** → scout → detective → tech-spec → quick-dev

---

**CRITICAL: MCP REQUIRED: tmux-cli**
**CRITICAL: Scout is the ONLY fixed stage — everything else is dynamic**
**CRITICAL: Each stage runs in its OWN tmux window — never mix concerns**
**CRITICAL: No user interruption between stages — evaluate and execute**
