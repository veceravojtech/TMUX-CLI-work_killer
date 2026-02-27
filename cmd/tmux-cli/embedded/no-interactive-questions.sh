#!/usr/bin/env bash
#
# no-interactive-questions.sh
# PreToolUse hook that blocks AskUserQuestion interactive widget calls.
#
# Claude Code sends tool input as JSON on stdin.
# We drain stdin, then output a block decision so Claude falls back to plain text.
# Exit 2 = block the tool call.

# Drain stdin (required — Claude sends tool JSON here)
cat > /dev/null

# Output block decision
printf '{"decision":"block","reason":"AskUserQuestion interactive widget is not supported in tmux worker windows. Write your questions as plain text in your response instead."}\n'

exit 2
