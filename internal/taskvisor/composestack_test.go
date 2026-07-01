package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// composeCall records one invocation of the injected ComposeRunnerFunc so tests
// can assert argv + cwd WITHOUT a Docker daemon present.
type composeCall struct {
	dir  string
	args []string
}

// fakeComposeRunner is the test double mirroring the ScriptRunnerFunc seam: it
// records every call and returns a configurable exit code / error keyed off argv.
type fakeComposeRunner struct {
	calls     []composeCall
	exitFor   func(args []string) (int, error)
	stderrFor func(args []string) string // injected stderr keyed off argv; "" when nil
}

func (f *fakeComposeRunner) run(_ context.Context, dir string, _ []string, args ...string) (string, string, int, error) {
	f.calls = append(f.calls, composeCall{dir: dir, args: append([]string(nil), args...)})
	var stderr string
	if f.stderrFor != nil {
		stderr = f.stderrFor(args)
	}
	if f.exitFor != nil {
		code, err := f.exitFor(args)
		return "", stderr, code, err
	}
	return "", stderr, 0, nil
}

// containsSeq reports whether needle appears as a contiguous subsequence of hay.
func containsSeq(hay []string, needle ...string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func writeComposeFile(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("services: {}\n"), 0o644))
}

// TC-1
func TestWorktreeComposeProject_DerivesName(t *testing.T) {
	assert.Equal(t, "taskvisor-goal-015", WorktreeComposeProject("goal-015"))
}

// TC-2
func TestWorktreeComposeProject_NormalizesInvalidChars(t *testing.T) {
	// Prefix is added BEFORE normalize so the taskvisor- guard token survives.
	assert.Equal(t, "taskvisor-goal15", WorktreeComposeProject("Goal/15!"))
}

// TC-3
func TestComposeStackUp_RunsUpDetachedWithWorktreeCwd(t *testing.T) {
	wt := t.TempDir()
	writeComposeFile(t, wt, "docker-compose.yml")
	fake := &fakeComposeRunner{}
	s := NewComposeStack(ExecRuntime{RunTarget: "docker", AppSvc: "app"}, "goal-015", wt, "", fake.run)

	require.NoError(t, s.Up(context.Background()))

	require.NotEmpty(t, fake.calls)
	first := fake.calls[0]
	assert.Equal(t, wt, first.dir, "up must run with cwd=worktree")
	assert.Equal(t,
		[]string{"compose", "-p", "taskvisor-goal-015", "-f", s.BaseFile, "-f", s.OverrideFile, "up", "-d"},
		first.args,
	)
}

// TC-4
func TestComposeStackUp_WritesPortStripOverride(t *testing.T) {
	wt := t.TempDir()
	writeComposeFile(t, wt, "docker-compose.yml")
	fake := &fakeComposeRunner{}
	s := NewComposeStack(ExecRuntime{RunTarget: "docker", AppSvc: "app"}, "goal-015", wt, "", fake.run)

	require.NoError(t, s.Up(context.Background()))

	data, err := os.ReadFile(filepath.Join(wt, "docker-compose.taskvisor.override.yml"))
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, "app:")
	assert.Contains(t, body, "db:")
	assert.Contains(t, body, "ports: !reset []")
}

// TC-5
func TestComposeStackUp_RunsBaselineMigrateWhenSet(t *testing.T) {
	wt := t.TempDir()
	writeComposeFile(t, wt, "docker-compose.yml")
	fake := &fakeComposeRunner{}
	const baseline = "bin/console doctrine:migrations:migrate -n"
	s := NewComposeStack(ExecRuntime{RunTarget: "docker", AppSvc: "app"}, "goal-015", wt, baseline, fake.run)

	require.NoError(t, s.Up(context.Background()))

	require.Len(t, fake.calls, 2, "up -d then baseline exec")
	assert.True(t, containsSeq(fake.calls[0].args, "up", "-d"), "first call is up -d")
	assert.True(t,
		containsSeq(fake.calls[1].args, "exec", "-T", "app", "sh", "-c", baseline),
		"baseline migrate runs via exec -T after up -d: %v", fake.calls[1].args,
	)
}

// TC-6
func TestComposeStackUp_SkipsMigrateWhenBaselineEmpty(t *testing.T) {
	wt := t.TempDir()
	writeComposeFile(t, wt, "docker-compose.yml")
	fake := &fakeComposeRunner{}
	s := NewComposeStack(ExecRuntime{RunTarget: "docker", AppSvc: "app"}, "goal-015", wt, "", fake.run)

	require.NoError(t, s.Up(context.Background()))

	for _, c := range fake.calls {
		assert.False(t, containsSeq(c.args, "exec"), "no exec call when baseline empty: %v", c.args)
	}
}

// TC-7
func TestComposeStackUp_ErrorsWhenNoBaseComposeFile(t *testing.T) {
	wt := t.TempDir() // no docker-compose.y*ml present
	fake := &fakeComposeRunner{}
	s := NewComposeStack(ExecRuntime{RunTarget: "docker", AppSvc: "app"}, "goal-015", wt, "", fake.run)

	err := s.Up(context.Background())
	require.Error(t, err)
	assert.Empty(t, fake.calls, "no compose call when base file missing")
}

// TC-8
func TestComposeStackDown_RunsDownWithVolumes(t *testing.T) {
	wt := t.TempDir()
	writeComposeFile(t, wt, "docker-compose.yml")
	fake := &fakeComposeRunner{}
	s := NewComposeStack(ExecRuntime{RunTarget: "docker", AppSvc: "app"}, "goal-015", wt, "", fake.run)

	require.NoError(t, s.Down(context.Background()))

	require.Len(t, fake.calls, 1)
	assert.Equal(t, wt, fake.calls[0].dir)
	assert.Equal(t,
		[]string{"compose", "-p", "taskvisor-goal-015", "down", "-v"},
		fake.calls[0].args,
	)
}

// TC-9
func TestComposeStackDown_RefusesNonTaskvisorProject(t *testing.T) {
	wt := t.TempDir()
	writeComposeFile(t, wt, "docker-compose.yml")
	fake := &fakeComposeRunner{}
	s := NewComposeStack(ExecRuntime{RunTarget: "docker", AppSvc: "app"}, "goal-015", wt, "", fake.run)
	s.Project = "productivitytool" // simulate a misresolved base project name

	err := s.Down(context.Background())
	require.Error(t, err)
	assert.Empty(t, fake.calls, "must issue NO down -v against a non-taskvisor project")
}

// TC-10
func TestResolveBaselineCmd_ParsesDocumentedField(t *testing.T) {
	body := "## Stack\n**Stack Baseline:** bin/console doctrine:migrations:migrate -n\n"
	assert.Equal(t, "bin/console doctrine:migrations:migrate -n", resolveBaselineCmd(body))

	assert.Equal(t, "", resolveBaselineCmd("no documented field here\n"))

	// Alternate documented label.
	alt := "Baseline Command: composer run-script setup\n"
	assert.Equal(t, "composer run-script setup", resolveBaselineCmd(alt))
}

// TC-11
func TestResolveComposeProject_NoWorktreePathUnchanged(t *testing.T) {
	root := t.TempDir() // no test-environment.md documented field, no .env
	got := resolveComposeProject(root, "")
	want := normalizeComposeName(filepath.Base(NormalizeProjectDir(root)))
	assert.Equal(t, want, got, "no-worktree base resolution must stay byte-identical")
}

// TC-12
func TestComposeStackUp_PropagatesRunnerNonZeroExit(t *testing.T) {
	wt := t.TempDir()
	writeComposeFile(t, wt, "docker-compose.yml")
	fake := &fakeComposeRunner{
		exitFor: func(args []string) (int, error) {
			if containsSeq(args, "up", "-d") {
				return 1, nil
			}
			return 0, nil
		},
	}
	s := NewComposeStack(ExecRuntime{RunTarget: "docker", AppSvc: "app"}, "goal-015", wt, "", fake.run)

	err := s.Up(context.Background())
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "exit")
}

// TC-13: a DB-less / no-registered-migrations Symfony worktree makes the baseline
// migrate exit non-zero with a "nothing to do" stderr. Up must treat that as a
// logged no-op and return nil so the goal stack brings up cleanly instead of
// poll-wedging (backend task 324).
func TestComposeStackUp_ToleratesNoMigrations(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
	}{
		{
			name:   "no migrations registered",
			stderr: "No migrations to execute.",
		},
		{
			name:   "migrations namespace absent",
			stderr: `There are no commands defined in the "doctrine:migrations" namespace`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wt := t.TempDir()
			writeComposeFile(t, wt, "docker-compose.yml")
			fake := &fakeComposeRunner{
				exitFor: func(args []string) (int, error) {
					if containsSeq(args, "exec") {
						return 1, nil
					}
					return 0, nil
				},
				stderrFor: func(args []string) string {
					if containsSeq(args, "exec") {
						return tc.stderr
					}
					return ""
				},
			}
			s := NewComposeStack(ExecRuntime{RunTarget: "docker", AppSvc: "app"}, "goal-015", wt,
				"bin/console doctrine:migrations:migrate -n", fake.run)

			require.NoError(t, s.Up(context.Background()),
				"benign baseline-migrate stderr must be tolerated as a no-op")
		})
	}
}

// TC-14: a genuine migration failure (real SQL/connection error) on the baseline
// migrate step must stay fatal — the tolerance branch must NOT swallow it.
func TestComposeStackUp_GenuineBaselineMigrateFailureStaysFatal(t *testing.T) {
	wt := t.TempDir()
	writeComposeFile(t, wt, "docker-compose.yml")
	fake := &fakeComposeRunner{
		exitFor: func(args []string) (int, error) {
			if containsSeq(args, "exec") {
				return 1, nil
			}
			return 0, nil
		},
		stderrFor: func(args []string) string {
			if containsSeq(args, "exec") {
				return "SQLSTATE[08006] connection refused"
			}
			return ""
		},
	}
	s := NewComposeStack(ExecRuntime{RunTarget: "docker", AppSvc: "app"}, "goal-015", wt,
		"bin/console doctrine:migrations:migrate -n", fake.run)

	err := s.Up(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "baseline migrate failed")
}

// TC-15: unit test of the pure helper — true ONLY for the two documented
// "nothing to do" fragments (case-insensitive, tolerant of surrounding text /
// punctuation); false for empty and for real failures.
func TestBaselineMigrateBenign_MatchesBenignFragmentsOnly(t *testing.T) {
	assert.True(t, baselineMigrateBenign("No migrations to execute."))
	assert.True(t, baselineMigrateBenign("[notice] No migrations to execute."))
	assert.True(t, baselineMigrateBenign(`There are no commands defined in the "doctrine:migrations" namespace`))
	assert.True(t, baselineMigrateBenign(`  In Application.php: There are no commands defined in the "doctrine:migrations" namespace.`))

	assert.False(t, baselineMigrateBenign(""))
	assert.False(t, baselineMigrateBenign("SQLSTATE[08006] connection refused"))
	assert.False(t, baselineMigrateBenign("An exception occurred while executing a query"))
}
