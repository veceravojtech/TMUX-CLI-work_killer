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

// ValidStatuses is the single source of truth for the closed set of backend task
// statuses (the lifecycle plus the administrative terminal states). It is used to
// validate the optional status filter on the task-list/query tools. Advancing a
// task's status is narrower — see ValidWorkerStatusTargets.
var ValidStatuses = map[string]bool{
	"new":         true,
	"claimed":     true,
	"in_progress": true,
	"resolved":    true,
	"failed":      true,
	"denied":      true,
	"archived":    true,
}

// ValidWorkerStatusTargets is the set of statuses a claiming worker may set via
// PATCH /api/v1/tasks/{id}/status. "claimed" is reached only through the claim
// endpoint; "new"/"denied"/"archived" are not worker-settable. The backend still
// enforces the from->to transition (and returns ErrInvalidTransition); this set
// only gives the agent a fast, clear rejection for an obviously wrong target.
var ValidWorkerStatusTargets = map[string]bool{
	"in_progress": true,
	"resolved":    true,
	"failed":      true,
}

// ValidAdminStatusTargets is the set of terminal statuses an id-targeted admin
// tool may set out-of-band (no claim required) via the task /deny, /resolve, and
// /archive endpoints. It is the single source of truth for the MCP
// task-set-status tool — distinct from ValidWorkerStatusTargets, which is the
// claim-gated worker advance set. Worker lifecycle statuses
// (new/claimed/in_progress/failed) are deliberately excluded.
var ValidAdminStatusTargets = map[string]bool{
	"denied":   true,
	"resolved": true,
	"archived": true,
}
