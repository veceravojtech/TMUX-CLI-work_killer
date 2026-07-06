package tasks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// researchModeCase is one row of the sizing-gate truth table: an input brief
// and the branch ComputeResearchMode must deterministically produce for it.
type researchModeCase struct {
	name        string
	in          ResearchModeInput
	wantMode    ResearchMode
	wantPrecise bool
}

func (tc researchModeCase) run(t *testing.T) {
	t.Helper()
	decision := ComputeResearchMode(tc.in)
	assert.Equal(t, tc.wantMode, decision.Mode)
	assert.Equal(t, tc.wantPrecise, decision.Precise)
	assert.NotEmpty(t, decision.Reason, "every decision must carry a reason for the XML LOG line")
}

// TestResearchModeInlineForPreciseBrief is the REQUIRED test proving that a
// precise brief (named file + named symbol + concrete edit) resolves to inline
// regardless of how large the candidate file(s) are — candidate-file LOC is
// IGNORED on the precise branch (the task-474 fix).
func TestResearchModeInlineForPreciseBrief(t *testing.T) {
	researchModeCase{
		in: ResearchModeInput{
			NamedFiles:       []string{"stage-1-context.xml"},
			NamedSymbols:     []string{"substep 1.2b"},
			HasConcreteEdit:  true,
			CandidateFileLOC: 1553, // > 500; must NOT force spawn on the precise branch
		},
		wantMode:    ResearchModeInline,
		wantPrecise: true,
	}.run(t)
}

// TestComputeResearchMode_UnmeasurableBriefSpawns asserts the fail-safe: a
// non-precise brief with no measurable estimate spawns (never under-provision).
func TestComputeResearchMode_UnmeasurableBriefSpawns(t *testing.T) {
	researchModeCase{
		in:          ResearchModeInput{Measurable: false},
		wantMode:    ResearchModeSpawn,
		wantPrecise: false,
	}.run(t)
}

// TestComputeResearchMode_PreciseBeatsUnmeasurable asserts the ordering: the
// precise short-circuit precedes the !Measurable fail-safe, so a precise brief
// with no numeric estimate still inlines.
func TestComputeResearchMode_PreciseBeatsUnmeasurable(t *testing.T) {
	researchModeCase{
		in: ResearchModeInput{
			NamedFiles:      []string{"x.go"},
			NamedSymbols:    []string{"Foo"},
			HasConcreteEdit: true,
			Measurable:      false,
		},
		wantMode:    ResearchModeInline,
		wantPrecise: true,
	}.run(t)
}

// TestComputeResearchMode_SmallImpliedChangeInline asserts a measurable,
// non-precise brief within both thresholds inlines.
func TestComputeResearchMode_SmallImpliedChangeInline(t *testing.T) {
	researchModeCase{
		in: ResearchModeInput{
			Measurable:          true,
			ImpliedChangedLines: 130,
			ImpliedTouchedFiles: 3,
		},
		wantMode:    ResearchModeInline,
		wantPrecise: false,
	}.run(t)
}

// TestComputeResearchMode_LargeImpliedChangeSpawns asserts a measurable,
// non-precise brief exceeding the changed-lines threshold spawns.
func TestComputeResearchMode_LargeImpliedChangeSpawns(t *testing.T) {
	researchModeCase{
		in: ResearchModeInput{
			Measurable:          true,
			ImpliedChangedLines: 640,
			ImpliedTouchedFiles: 3,
		},
		wantMode:    ResearchModeSpawn,
		wantPrecise: false,
	}.run(t)
}

// TestComputeResearchMode_GreenfieldInline asserts a measurable net-new brief
// (0 implied lines / 0 files) inlines — matching the original greenfield note.
func TestComputeResearchMode_GreenfieldInline(t *testing.T) {
	researchModeCase{
		in: ResearchModeInput{
			Measurable:          true,
			ImpliedChangedLines: 0,
			ImpliedTouchedFiles: 0,
		},
		wantMode:    ResearchModeInline,
		wantPrecise: false,
	}.run(t)
}
