package org

import "time"

type HistoryBackfillSummary struct {
	Processed  int64            `json:"processed"`
	Recorded   int64            `json:"recorded"`
	Unassigned int64            `json:"unassigned"`
	Unknown    int64            `json:"unknown"`
	MissingSLA int64            `json:"missing_sla"`
	Reasons    map[string]int64 `json:"reasons"`
}
type HistoryBackfillSample struct {
	HistoryID      int64     `json:"history_id"`
	FindingID      int64     `json:"finding_id"`
	Kind           string    `json:"kind"`
	OccurredAt     time.Time `json:"occurred_at"`
	AssetID        *int64    `json:"asset_id"`
	DepartmentID   *int64    `json:"department_id"`
	BusinessLineID *int64    `json:"business_line_id"`
	OwnerID        *int64    `json:"owner_id"`
	Quality        string    `json:"quality"`
	Reason         string    `json:"reason"`
	SLADays        *int      `json:"sla_days"`
}
type HistoryBackfillPreview struct {
	RunID                        string                  `json:"run_id,omitempty"`
	UpperHistoryID               int64                   `json:"upper_history_id"`
	Cursor                       int64                   `json:"cursor"`
	Remaining                    int64                   `json:"remaining"`
	CurrentRemaining             int64                   `json:"current_remaining"`
	Done                         bool                    `json:"done"`
	StartedAt                    *time.Time              `json:"started_at"`
	FinishedAt                   *time.Time              `json:"finished_at"`
	Summary                      HistoryBackfillSummary  `json:"summary"`
	Candidates                   HistoryBackfillSummary  `json:"candidates"`
	Samples                      []HistoryBackfillSample `json:"samples"`
	UnresolvedSamples            []HistoryBackfillSample `json:"unresolved_samples"`
	CurrentSingleAssetCandidates int64                   `json:"current_single_asset_candidates"`
	AmbiguousCurrentFindings     int64                   `json:"ambiguous_current_findings"`
	UnregisteredAssets           int64                   `json:"unregistered_assets"`
	LegacyBusinessLines          int64                   `json:"legacy_business_lines"`
	LegacyPeople                 int64                   `json:"legacy_people"`
}
type HistoryBackfillBatch struct {
	RunID            string                 `json:"run_id,omitempty"`
	BatchOperationID string                 `json:"batch_operation_id,omitempty"`
	UpperHistoryID   int64                  `json:"upper_history_id"`
	Cursor           int64                  `json:"cursor"`
	Processed        int                    `json:"processed"`
	Remaining        int64                  `json:"remaining"`
	CurrentRemaining int64                  `json:"current_remaining"`
	Done             bool                   `json:"done"`
	Summary          HistoryBackfillSummary `json:"summary"`
}
