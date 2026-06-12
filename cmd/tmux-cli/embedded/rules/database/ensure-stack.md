# Ensure-stack convention (pack: database — HAS_DATABASE)

Binding planning convention, loaded when the project has a database.
Extracted verbatim from the planner's `<conventions>` block.

<rule critical="true" id="ENSURE-STACK-CONV" condition="HAS_DATABASE">ENSURE-STACK CONVENTION — BINDING ON EVERY LATER STEP. Two parts: (1) SCAFFOLD PRODUCER — goal-002 MUST deliver bin/ensure-test-stack.sh (executable, #!/bin/sh -e, three phases: stack up → test-env migrations → test fixtures); acceptance criterion: `test -x bin/ensure-test-stack.sh`. The script body comes from the language template (.tmux-cli/templates/{{LANG}}-{{FRAMEWORK}}/fixtures.md "Ensure-stack script" section). (2) CONSUMER — every goal whose validate contains an E2E or host-HTTP probe MUST list `bash bin/ensure-test-stack.sh` as a SEPARATE validate line immediately BEFORE the probe line. The daemon runs each validate line independently; never &amp;&amp;-join the ensure-stack invocation with any other command.</rule>
