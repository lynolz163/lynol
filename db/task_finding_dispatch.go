package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

var (
	ErrTaskNotFound     = errors.New("任务不存在或已归档")
	ErrTaskNotCompleted = errors.New("仅已完成的任务可以批量记录下发时间")
)

type TaskFindingDispatchPreview struct {
	TaskID            string         `json:"task_id"`
	TaskName          string         `json:"task_name"`
	TaskStatus        string         `json:"task_status"`
	Total             int            `json:"total"`
	Eligible          int            `json:"eligible"`
	AlreadyDispatched int            `json:"already_dispatched"`
	SkippedStatus     int            `json:"skipped_status"`
	InvalidTime       int            `json:"invalid_time"`
	CanDispatch       bool           `json:"can_dispatch"`
	Reason            string         `json:"reason,omitempty"`
	SeverityCounts    map[string]int `json:"severity_counts"`
}

type TaskFindingDispatchResult struct {
	TaskID            string     `json:"task_id"`
	Recorded          int        `json:"recorded"`
	Skipped           int        `json:"skipped"`
	AlreadyDispatched int        `json:"already_dispatched"`
	SkippedStatus     int        `json:"skipped_status"`
	RecordedAt        *time.Time `json:"recorded_at"`
}

// PreviewTaskFindingDispatch reads the complete set of findings owned by a task.
// Inherited findings belong to their source task and are intentionally excluded.
func (d *DB) PreviewTaskFindingDispatch(ctx context.Context, taskID int64, recordedAt *time.Time) (TaskFindingDispatchPreview, error) {
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return TaskFindingDispatchPreview{}, err
	}
	defer tx.Rollback()
	preview, _, _, err := taskFindingDispatchTx(ctx, tx, taskID, recordedAt, false)
	return preview, err
}

// DispatchTaskFindings records one timestamp for this task's eligible findings.
// Selection, chronology checks and journal writes are committed together.
func (d *DB) DispatchTaskFindings(ctx context.Context, taskID int64, recordedAt *time.Time) (TaskFindingDispatchResult, error) {
	result := TaskFindingDispatchResult{TaskID: strconv.FormatInt(taskID, 10)}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	preview, ids, chosenAt, err := taskFindingDispatchTx(ctx, tx, taskID, recordedAt, true)
	if err != nil {
		return result, err
	}
	if preview.TaskStatus != "done" {
		return result, ErrTaskNotCompleted
	}
	if preview.InvalidTime > 0 {
		return result, fmt.Errorf("%w：%s", ErrInvalidFindingMilestoneTime, preview.Reason)
	}
	recorded, err := writeFindingMilestonesTx(ctx, tx, ids, "dispatched_at", chosenAt)
	if err != nil {
		return result, err
	}
	result.Recorded = len(recorded)
	result.Skipped = preview.Total - result.Recorded
	result.AlreadyDispatched = preview.AlreadyDispatched
	result.SkippedStatus = preview.SkippedStatus
	if result.Recorded > 0 {
		result.RecordedAt = &chosenAt
	}
	return result, tx.Commit()
}

func taskFindingDispatchTx(ctx context.Context, tx *sql.Tx, taskID int64, recordedAt *time.Time, lock bool) (TaskFindingDispatchPreview, []int64, time.Time, error) {
	p := TaskFindingDispatchPreview{
		TaskID:         strconv.FormatInt(taskID, 10),
		SeverityCounts: map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0},
	}
	query := `SELECT COALESCE(name,''),status,now() FROM tasks WHERE id=$1 AND deleted_at IS NULL`
	if lock {
		query += ` FOR UPDATE`
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, query, taskID).Scan(&p.TaskName, &p.TaskStatus, &now); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrTaskNotFound
		}
		return p, nil, time.Time{}, err
	}
	chosenAt := now
	if recordedAt != nil {
		chosenAt = *recordedAt
	}
	query = `SELECT f.id,f.status,f.severity,f.created_at,f.dispatched_at,f.developer_fixed_at,` + findingRemediationCycleSQL + `,
		(f.dispatched_at IS NULL OR ` + fmt.Sprintf(findingNewAttempt, "f.dispatched_at") + `)
		FROM findings f WHERE f.task_id=$1 ORDER BY f.id`
	if lock {
		query += ` FOR UPDATE OF f`
	}
	rows, err := tx.QueryContext(ctx, query, taskID)
	if err != nil {
		return p, nil, chosenAt, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		var status, severity string
		var discovered, cycle time.Time
		var dispatched, fixed *time.Time
		var missing bool
		if err := rows.Scan(&id, &status, &severity, &discovered, &dispatched, &fixed, &cycle, &missing); err != nil {
			return p, nil, chosenAt, err
		}
		p.Total++
		switch status {
		case FindingPending, FindingInProgress, FindingConfirmed:
		default:
			p.SkippedStatus++
			continue
		}
		if !missing {
			p.AlreadyDispatched++
			continue
		}
		ids = append(ids, id)
		p.Eligible++
		p.SeverityCounts[severity]++
		if chosenAt.After(now) || validateFindingMilestoneTime(id, "dispatched", chosenAt, discovered, cycle, dispatched, fixed) != nil {
			p.InvalidTime++
		}
	}
	if err := rows.Err(); err != nil {
		return p, nil, chosenAt, err
	}
	switch {
	case p.TaskStatus != "done":
		p.Reason = ErrTaskNotCompleted.Error()
	case p.Eligible == 0:
		p.Reason = "本任务没有本轮尚未下发的待处置漏洞"
	case chosenAt.After(now):
		p.Reason = "下发时间不能晚于当前时间，请调整下发时间"
	case p.InvalidTime > 0:
		p.Reason = fmt.Sprintf("%d 个漏洞的下发时间早于发现或本轮重开，或晚于本轮开发反馈修复时间，请调整下发时间", p.InvalidTime)
	default:
		p.CanDispatch = true
	}
	return p, ids, chosenAt, nil
}
