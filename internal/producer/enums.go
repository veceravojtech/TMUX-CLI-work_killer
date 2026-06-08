package producer

// ValidCategories is the single source of truth for the closed set of backend
// task categories. Every caller that validates or coerces a category (the
// daemon's normalizeCategory and the MCP task-report tool) MUST reference this
// set rather than redeclaring its own literal, so the contract stays in one
// place. Membership is "value is accepted as-is"; out-of-set handling (reject vs
// coerce) is the caller's policy, not this set's.
var ValidCategories = map[string]bool{
	"plan":       true,
	"supervisor": true,
	"validator":  true,
	"execute":    true,
	"general":    true,
}

// ValidSeverities is the single source of truth for the closed set of backend
// task severities. See ValidCategories for the single-source rationale; the same
// reuse rule applies here.
var ValidSeverities = map[string]bool{
	"critical": true,
	"warning":  true,
	"info":     true,
}
