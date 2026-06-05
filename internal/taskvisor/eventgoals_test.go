package taskvisor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveEmissionInvestigator_PhaseEvent_AddsInvestigator(t *testing.T) {
	inv, ok := deriveEmissionInvestigator(
		"event",
		"Catalog reserves stock",
		[]string{`src/Catalog/ constructs App\Share\Event\StockReserved`},
		[]string{"go build ./..."},
	)
	require.True(t, ok)
	assert.Equal(t, "emission-check", inv.Type)
	assert.Equal(t, "Event emission", inv.Name)
	require.Len(t, inv.Commands, 1)
	assert.Contains(t, inv.Commands[0], "StockReserved")
	assert.Contains(t, inv.Commands[0], "src/Catalog/")
	assert.Equal(t, []string{"src/Catalog/"}, inv.Paths)
}

func TestDeriveEmissionInvestigator_EventFQCNInAcceptance_Detected(t *testing.T) {
	inv, ok := deriveEmissionInvestigator(
		"",
		"Pricing recalculates",
		[]string{`emits App\Share\Event\PriceChanged from src/Pricing/`},
		nil,
	)
	require.True(t, ok)
	assert.Contains(t, inv.Pass, "PriceChanged")
	assert.Contains(t, inv.Fail, "PriceChanged")
	assert.Contains(t, inv.Commands[0], "src/Pricing/")
}

func TestDeriveEmissionInvestigator_NonEventGoal_NotAdded(t *testing.T) {
	_, ok := deriveEmissionInvestigator(
		"domain",
		"Add tax calculation",
		[]string{"compute totals in src/Pricing/"},
		[]string{"vendor/bin/phpstan analyse"},
	)
	assert.False(t, ok)
}

func TestDeriveEmissionInvestigator_GrepRootExcludesTests(t *testing.T) {
	inv, ok := deriveEmissionInvestigator(
		"event",
		"reserve",
		[]string{`src/Catalog/ emits App\Share\Event\StockReserved`},
		nil,
	)
	require.True(t, ok)
	assert.Contains(t, inv.Commands[0], "src/Catalog/")
	assert.NotContains(t, inv.Commands[0], "tests/")
	assert.NotContains(t, strings.Join(inv.Paths, ","), "tests/")
}

func TestDeriveEmissionInvestigator_EmptyCondition(t *testing.T) {
	inv, ok := deriveEmissionInvestigator(
		"choreography",
		`x emits App\Share\Event\StockReserved`,
		nil,
		nil,
	)
	require.True(t, ok)
	assert.Equal(t, "", inv.Condition)
}
