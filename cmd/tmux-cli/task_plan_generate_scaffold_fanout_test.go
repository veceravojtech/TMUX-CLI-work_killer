package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate_Sc18InFanoutTask2(t *testing.T) {
	content := readGenerateBundle(t)
	task2 := sliceBetween(t, content, `n="2" name="Config files and quality tools"`, `n="3" name="Ensure-stack script"`)
	assert.Contains(t, task2, "SC-18",
		"task-2 fan-out criteria must include SC-18")
	assert.Contains(t, task2, "APP_ENV=test",
		"task-2 scope must mention APP_ENV=test pinning")
}

func TestTemplate_GoalCreateAcceptanceListsSc21(t *testing.T) {
	content := readGenerateBundle(t)
	goalCreate := sliceBetween(t, content, `n="2.7" title="Call goal-create MCP"`, `condition="goal-create returns error"`)
	assert.Contains(t, goalCreate, "SC-21",
		"goal-create acceptance param in substep 2.7 must include SC-21")
}

func TestTemplate_FanoutHeaderCountsFourTasks(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "4 file-disjoint tasks",
		"fan-out header must say 4 file-disjoint tasks")
	assert.NotContains(t, content, "3 file-disjoint tasks",
		"fan-out header must no longer say 3 file-disjoint tasks")
}

func TestTemplate_SuccessPrintCountsFourTasks(t *testing.T) {
	content := readGenerateBundle(t)
	scaffoldStep := sliceBetween(t, content, `n="2" title="Generate Scaffold goal`, `</step>`)
	successPrint := sliceBetween(t, scaffoldStep, `condition="goal-create succeeds"`, `</check>`)
	assert.Contains(t, successPrint, "4 fan-out tasks",
		"success print after goal-create must say 4 fan-out tasks")
	assert.NotContains(t, successPrint, "3 fan-out tasks",
		"success print must no longer say 3 fan-out tasks")
}

func TestTemplate_SpineSummaryCountsFourTasks(t *testing.T) {
	spine := readTaskPlanGenerateTemplate(t)
	step2Summary := sliceBetween(t, spine, "Step 2 (Scaffold)", "Step 3")
	assert.Contains(t, step2Summary, "4 fan-out tasks",
		"spine step-2 summary must say 4 fan-out tasks")
	assert.NotContains(t, step2Summary, "3 fan-out tasks",
		"spine step-2 summary must no longer say 3 fan-out tasks")
}

func TestTemplate_Sc21HasValidateCmd(t *testing.T) {
	content := readGenerateBundle(t)
	validateBlock := sliceBetween(t, content, `n="2.4" title="Compose validation commands"`, `n="2.5"`)
	assert.Contains(t, validateBlock, `source="SC-21"`,
		"substep 2.4 must have a validate cmd for SC-21")
}

func TestTemplate_FanoutHeaderMentionsTaskR(t *testing.T) {
	content := readGenerateBundle(t)
	header := sliceBetween(t, content, `Build fan-out hints for multi-task execution`, `n="0" name="Composer setup"`)
	assert.Contains(t, header, "task-R",
		"fan-out header must mention task-R conditional")
	assert.Contains(t, header, "DOCKER-RUNTIME-FRONTLOAD",
		"fan-out header must reference DOCKER-RUNTIME-FRONTLOAD convention")
}

func TestTemplate_SuccessPrintMentionsTaskR(t *testing.T) {
	content := readGenerateBundle(t)
	scaffoldStep := sliceBetween(t, content, `n="2" title="Generate Scaffold goal`, `</step>`)
	successPrint := sliceBetween(t, scaffoldStep, `condition="goal-create succeeds"`, `</check>`)
	assert.Contains(t, successPrint, "task-R",
		"success print after goal-create must mention task-R conditional")
}

func TestTemplate_FileDisjointRuleOwnsTaskR(t *testing.T) {
	content := readGenerateBundle(t)
	disjointRule := sliceBetween(t, content, "File-disjoint:", "Zero overlap.")
	assert.Contains(t, disjointRule, "task-R",
		"file-disjoint ownership rule must include task-R")
	assert.Contains(t, disjointRule, "docker-compose.yaml",
		"task-R ownership must include docker-compose.yaml")
}

func TestMD_FanoutTableScaffoldRowMentionsTask3(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	var scaffoldRow string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "| Scaffold |") && strings.Contains(line, "Multi-task") {
			scaffoldRow = line
			break
		}
	}
	require.NotEmpty(t, scaffoldRow, "Fan-out table must have a Scaffold Multi-task row")
	assert.Contains(t, scaffoldRow, "task-3",
		"Scaffold row must enumerate task-3")
	assert.Contains(t, scaffoldRow, "ensure-test-stack.sh",
		"Scaffold row must mention ensure-test-stack.sh deliverable")
}

func TestMD_FanoutTableScaffoldCountMatchesXML(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	var scaffoldRow string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "| Scaffold |") && strings.Contains(line, "Multi-task") {
			scaffoldRow = line
			break
		}
	}
	require.NotEmpty(t, scaffoldRow, "Fan-out table must have a Scaffold Multi-task row")
	assert.Contains(t, scaffoldRow, "Multi-task (4",
		"Scaffold row count must start with 4 (matching xml)")
	assert.NotContains(t, scaffoldRow, "Multi-task (3",
		"Scaffold row must no longer say Multi-task (3")
}

func TestMD_FanoutTableInfraRowMentionsTaskLast(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	var infraRow string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "| Infrastructure (>3 entities) |") {
			infraRow = line
			break
		}
	}
	require.NotEmpty(t, infraRow, "Fan-out table must have an Infrastructure (>3 entities) row")
	assert.Contains(t, infraRow, "task-last",
		"Infrastructure row must enumerate task-last tier")
	assert.Contains(t, infraRow, "migrations",
		"Infrastructure row must mention migrations in task-last")
}

func TestMD_FanoutTableApplicationRowMentionsHandlerConditional(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	var appRow string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "| Application |") && strings.Contains(line, "Single-task") {
			appRow = line
			break
		}
	}
	require.NotEmpty(t, appRow, "Fan-out table must have an Application Single-task row")
	assert.Contains(t, appRow, ">6 handlers",
		"Application row Pattern cell must mention >6 handlers conditional")
}

func TestMD_FanoutTableFinalQualityRowOmitsDeptrac(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	var finalRow string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "| Final quality |") {
			finalRow = line
			break
		}
	}
	require.NotEmpty(t, finalRow, "Fan-out table must have a Final quality row")
	assert.NotContains(t, finalRow, "Deptrac",
		"Final quality row must not list Deptrac (it is a separate final-gate goal)")
}

func TestTemplate_InfraFanoutHintMentionsTaskLast(t *testing.T) {
	content := readGenerateBundle(t)
	var hintLine string
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "task-0 for shared infra") {
			hintLine = line
			break
		}
	}
	require.NotEmpty(t, hintLine, "Infrastructure xml must contain fan-out hint determination line with 'task-0 for shared infra'")
	assert.Contains(t, hintLine, "task-last",
		"Infrastructure fan-out hint determination must also mention task-last")
}

func TestTemplate_ValidateParamMentionsConditionals(t *testing.T) {
	content := readGenerateBundle(t)
	substep27 := sliceBetween(t, content, `n="2.7" title="Call goal-create MCP"`, `</substep>`)
	validateParam := sliceBetween(t, substep27, `name="validate"`, `</param>`)
	assert.Contains(t, validateParam, "conditional",
		"validate param in substep 2.7 must acknowledge conditional commands")
}
