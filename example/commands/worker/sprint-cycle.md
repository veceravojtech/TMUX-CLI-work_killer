---
description: 'Sprint cycle orchestrator: loops create-story → dev-story until all backlog stories are done.'
---

## Sprint Cycle Orchestrator

Loops two BMAD stages — create-story and dev-story — until no `backlog` stories remain in `sprint-status.yaml`. Each iteration produces one story file and implements it. Both skills auto-discover the next story from sprint-status.yaml.

**CRITICAL: Execute stages ONE BY ONE. Wait for each stage to fully complete before starting the next.**

### Iteration Stages

| Stage | Worker | Purpose | Expected Outcome |
|-------|--------|---------|------------------|
| A | /worker:command /bmad-bmm-create-story | Analyze artifacts, produce story file | Story status: backlog → ready-for-dev |
| B | /worker:command /bmad-bmm-dev-story | Implement story via red-green-refactor | Story status: ready-for-dev → review |
| C | Check sprint-status.yaml | Count remaining backlog stories | Decision: loop or exit |

### Execution Rules

1. **Create TODO list** with the following items at the start:
   - Read sprint-status.yaml — count initial backlog stories
   - Iteration 1 — Stage A: create-story
   - Iteration 1 — Stage B: dev-story
   - Iteration 1 — Stage C: check backlog
   - (further iterations added dynamically as the loop continues)
   - Print final sprint cycle report

2. **Initial gate**: Read `sprint-status.yaml` (look for `_bmad-output/implementation-artifacts/sprint-status.yaml` relative to project root). Parse `development_status`. Count stories with status `backlog` (story keys match pattern `N-N-*`, NOT `epic-*` and NOT `*-retrospective`). If zero backlog stories → **STOP**, nothing to do.

3. **Begin loop** — for each iteration:

   a. **Stage A — Create Story**: Execute `/worker:command /bmad-bmm-create-story`. Wait for full completion. Record which story was created.

   b. **Stage B — Dev Story**: Execute `/worker:command /bmad-bmm-dev-story`. Wait for full completion. Record which story was implemented.

   c. **Stage C — Backlog Check**: Read `sprint-status.yaml` again. Count remaining `backlog` stories (same filtering as step 2). Record the count.
      - If backlog stories remain → add next iteration stages to TODO list, continue loop.
      - If no backlog stories remain → exit loop, proceed to final report.

4. **Track across iterations**: Maintain a running list of:
   - Iteration number
   - Story key processed
   - Create-story result (success/fail)
   - Dev-story result (success/fail)

### Error Handling

- If **any stage fails** in any iteration: **STOP immediately**. Report which iteration and stage failed. Show the running summary of all iterations completed so far. Ask user how to proceed.

### Final Report

After the loop exits (no backlog stories remaining), print a summary:

```
## Sprint Cycle Complete

| Iteration | Story | Create Story | Dev Story |
|-----------|-------|--------------|-----------|
| 1         | ...   | ...          | ...       |
| 2         | ...   | ...          | ...       |
| ...       | ...   | ...          | ...       |

**Total stories processed:** N
**Remaining backlog:** 0
**Stories now in review:** N
```

**CRITICAL: MCP REQUIRED: tmux-cli**
