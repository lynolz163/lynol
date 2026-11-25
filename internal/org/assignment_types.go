package org

import (
	"time"
)

type AssetFilter struct {
	Status string `json:"status"`
	Type   string `json:"type"`
	Q      string `json:"q"`
	From   string `json:"from"`
	To     string `json:"to"`
}
type AssignmentSelection struct {
	Mode          string      `json:"mode"`
	IDs           []int64     `json:"ids,omitempty"`
	Filter        AssetFilter `json:"filter,omitempty"`
	ExpectedCount int         `json:"expected_count"`
}
type AssignmentRequest struct {
	Selection               AssignmentSelection `json:"selection"`
	DepartmentID            int64               `json:"department_id"`
	BusinessLineID          *int64              `json:"business_line_id"`
	Scope                   string              `json:"scope"`
	BackdateFirstAssignment bool                `json:"backdate_first_assignment"`
	GeneratedRules          []Rule              `json:"generated_rules"`
}
type AssignmentTransfer struct {
	DepartmentID   *int64 `json:"department_id"`
	BusinessLineID *int64 `json:"business_line_id"`
	Count          int    `json:"count"`
}
type AssignmentPreview struct {
	Token            string               `json:"token"`
	ExpiresAt        time.Time            `json:"expires_at"`
	AssetCount       int                  `json:"asset_count"`
	OpenCount        int64                `json:"open_count"`
	AffectedOpen     int64                `json:"affected_open"`
	Severity         map[string]int64     `json:"severity"`
	AlreadyAssigned  int                  `json:"already_assigned"`
	Transfers        []AssignmentTransfer `json:"transfers"`
	RuleConflicts    int                  `json:"rule_conflicts"`
	BackdateEligible bool                 `json:"backdate_eligible"`
	SuggestedRules   []Rule               `json:"suggested_rules"`
}
type AssignmentConfirm struct {
	Token              string `json:"token"`
	IdempotencyKey     string `json:"idempotency_key"`
	OverwriteConfirmed bool   `json:"overwrite_confirmed"`
	BackdateConfirmed  bool   `json:"backdate_confirmed"`
}
type AssignmentResult struct {
	OperationID string     `json:"operation_id"`
	JobID       string     `json:"job_id"`
	State       string     `json:"state"`
	Assigned    int        `json:"assigned"`
	Processed   int        `json:"processed"`
	Total       int        `json:"total"`
	EffectiveAt *time.Time `json:"effective_at,omitempty"`
}
type AssignmentUndoResult struct {
	OperationID string `json:"operation_id"`
	Mode        string `json:"mode"`
	Restored    int    `json:"restored"`
	Reversed    int    `json:"reversed"`
}
type AssignmentJob struct {
	ID           string     `json:"id"`
	OperationID  string     `json:"operation_id"`
	State        string     `json:"state"`
	Total        int        `json:"total"`
	Processed    int        `json:"processed"`
	Assigned     int        `json:"assigned"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	EffectiveAt  *time.Time `json:"effective_at"`
	ErrorCode    *string    `json:"error_code"`
	ErrorDetail  string     `json:"error_detail"`
	Acknowledged bool       `json:"acknowledged"`
	UndoMode     string     `json:"undo_mode,omitempty"`
}
type assignmentToken struct {
	Request          AssignmentRequest `json:"request"`
	Fingerprint      string            `json:"fingerprint"`
	ConfigRevision   int64             `json:"config_revision"`
	Expires          int64             `json:"expires"`
	AlreadyAssigned  int               `json:"already_assigned"`
	BackdateEligible bool              `json:"backdate_eligible"`
}
type assignmentMember struct {
	AssetID           int64     `json:"asset_id"`
	OwnershipRevision int64     `json:"ownership_revision"`
	EventRevision     int64     `json:"event_revision"`
	SourceUpdatedAt   time.Time `json:"source_updated_at"`
	FirstSeenAt       time.Time `json:"first_seen_at"`
	Registered        bool      `json:"registered"`
}
type assignmentPlan struct {
	Claim          string             `json:"-"`
	Token          assignmentToken    `json:"token"`
	ResolvedAssets []assignmentMember `json:"resolved_assets"`
}
