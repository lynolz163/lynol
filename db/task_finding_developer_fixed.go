package db

import (
	"context"
	"time"
)

type TaskFindingMilestonePreview struct {
	TaskID          string         `json:"task_id"`
	TaskName        string         `json:"task_name"`
	TaskStatus      string         `json:"task_status"`
	Total           int            `json:"total"`
	Eligible        int            `json:"eligible"`
	AlreadyRecorded int            `json:"already_recorded"`
	SkippedStatus   int            `json:"skipped_status"`
	InvalidTime     int            `json:"invalid_time"`
	CanRecord       bool           `json:"can_record"`
	Reason          string         `json:"reason,omitempty"`
	SeverityCounts  map[string]int `json:"severity_counts"`
}

type TaskFindingMilestoneResult struct {
	TaskID          string     `json:"task_id"`
	Recorded        int        `json:"recorded"`
	Skipped         int        `json:"skipped"`
	AlreadyRecorded int        `json:"already_recorded"`
	SkippedStatus   int        `json:"skipped_status"`
	RecordedAt      *time.Time `json:"recorded_at"`
}

func (d *DB) PreviewTaskFindingDeveloperFixed(ctx context.Context, taskID int64, recordedAt *time.Time) (TaskFindingMilestonePreview, error) {
	return d.previewTaskFindingMilestone(ctx, taskID, recordedAt, "developer_fixed")
}

// RecordTaskFindingDeveloperFixed records repair feedback for this remediation
// attempt. It does not close findings, start retests, or send notifications.
func (d *DB) RecordTaskFindingDeveloperFixed(ctx context.Context, taskID int64, recordedAt *time.Time) (TaskFindingMilestoneResult, error) {
	return d.recordTaskFindingMilestone(ctx, taskID, recordedAt, "developer_fixed")
}
