package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
)

// RunAssignmentWorker belongs to the host server lifecycle. Cancellation stops
// polling and rolls back an in-flight job; no goroutine is retained after return.
func (s *Store) RunAssignmentWorker(ctx context.Context) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		worked, err := s.ProcessNextAssignment(jobCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("[org assignments] %v", err)
		}
		if worked && err == nil && ctx.Err() == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
func (s *Store) ProcessNextAssignment(ctx context.Context) (bool, error) {
	claim := uuid.NewString()
	var job, op string
	var raw []byte
	err := s.DB.QueryRowContext(ctx, `WITH pending AS (SELECT j.id FROM org.asset_assignment_job j JOIN org.change_operation o ON o.id=j.operation_id WHERE (j.state='queued' OR (j.state='running' AND j.lease_until<clock_timestamp())) AND o.state<>'succeeded' ORDER BY j.created_at,j.id FOR UPDATE OF j SKIP LOCKED LIMIT 1)
 UPDATE org.asset_assignment_job j SET state='running',started_at=COALESCE(started_at,clock_timestamp()),lease_until=clock_timestamp()+interval '30 seconds',claimed_by=$1,processed_count=0 FROM pending WHERE j.id=pending.id RETURNING j.id,j.operation_id,j.selection`, claim).Scan(&job, &op, &raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var plan assignmentPlan
	if err = json.Unmarshal(raw, &plan); err == nil {
		plan.Claim = claim
		err = s.processAssignment(ctx, job, op, plan)
	}
	if err == nil {
		return true, nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		_, e := s.DB.ExecContext(cleanup, "UPDATE org.asset_assignment_job SET state='queued',lease_until=NULL,claimed_by=NULL,processed_count=0 WHERE id=$1 AND claimed_by=$2 AND state='running'", job, claim)
		if e != nil {
			log.Printf("[org assignments] release cancelled job: %v", e)
		}
	} else {
		code := "failed"
		message := "划归失败，请重新预览后重试"
		var api *APIError
		if errors.As(err, &api) {
			code = api.Code
			message = api.Message
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code = "timeout"
			message = "批次执行超时，整批已回滚，请缩小选择范围后重试"
		}
		result, e := s.DB.ExecContext(cleanup, "UPDATE org.asset_assignment_job SET state='failed',finished_at=clock_timestamp(),lease_until=NULL,error_code=$3,error_detail=$4,processed_count=0 WHERE id=$1 AND claimed_by=$2 AND state='running'", job, claim, code, jsonValue(map[string]string{"message": message}))
		if e != nil {
			log.Printf("[org assignments] record failure: %v", e)
		} else {
			n, _ := result.RowsAffected()
			if n == 1 {
				_, _ = s.DB.ExecContext(cleanup, "UPDATE org.change_operation SET state='failed',finished_at=clock_timestamp(),error_code=$2,error_detail=$3 WHERE id=$1", op, code, jsonValue(map[string]string{"message": message}))
			}
		}
	}
	return true, err
}
func (s *Store) processAssignment(ctx context.Context, job, op string, plan assignmentPlan) error {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Lease/progress use a separate connection; the ownership transaction never
	// holds the job row until its final state update immediately before commit.
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workCtx.Done():
				return
			case <-ticker.C:
				beat, stop := context.WithTimeout(workCtx, 3*time.Second)
				res, e := s.DB.ExecContext(beat, "UPDATE org.asset_assignment_job SET lease_until=clock_timestamp()+interval '30 seconds' WHERE id=$1 AND claimed_by=$2 AND state='running'", job, plan.Claim)
				stop()
				if e != nil {
					cancel()
					return
				}
				n, _ := res.RowsAffected()
				if n == 0 {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-heartbeatDone }()
	tx, err := s.DB.BeginTx(workCtx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(workCtx, "SELECT config_revision FROM org.sync_state WHERE singleton FOR UPDATE"); err != nil {
		return err
	}
	if err = assignmentTarget(workCtx, tx, plan.Token.Request); err != nil {
		return err
	}
	ids := make([]int64, 0, len(plan.ResolvedAssets))
	for _, m := range plan.ResolvedAssets {
		ids = append(ids, m.AssetID)
	}
	assets, err := selectAssignmentAssets(workCtx, tx, AssignmentSelection{Mode: "ids", IDs: ids, ExpectedCount: len(ids)})
	if err != nil {
		return err
	}
	for _, a := range assets {
		if a.Registered {
			if _, err = tx.ExecContext(workCtx, "SELECT asset_id FROM org.asset_registry WHERE asset_id=$1 FOR UPDATE", a.AssetID); err != nil {
				return err
			}
		}
	}
	_, hash, err := assignmentMembers(workCtx, tx, assets)
	if err != nil {
		return err
	}
	var revision int64
	if err = tx.QueryRowContext(workCtx, "SELECT config_revision FROM org.sync_state WHERE singleton").Scan(&revision); err != nil {
		return err
	}
	if hash != plan.Token.Fingerprint || revision != plan.Token.ConfigRevision {
		return conflict("数据已变化，请重新预览")
	}
	progress := func(n int) error {
		if n >= len(assets) {
			n = len(assets) - 1
		}
		progressCtx, stop := context.WithTimeout(workCtx, 3*time.Second)
		defer stop()
		res, e := s.DB.ExecContext(progressCtx, "UPDATE org.asset_assignment_job SET processed_count=$3,lease_until=clock_timestamp()+interval '30 seconds' WHERE id=$1 AND claimed_by=$2 AND state='running'", job, plan.Claim, n)
		if e != nil {
			return e
		}
		count, _ := res.RowsAffected()
		if count != 1 {
			return conflict("任务已由其他执行器接管")
		}
		return nil
	}
	if _, err = s.executeAssignment(workCtx, tx, job, op, plan, assets, progress); err != nil {
		return err
	}
	return tx.Commit()
}

const assignmentJobsSQL = `SELECT j.id,j.operation_id,j.state,j.expected_count,j.processed_count,CASE WHEN j.state='succeeded' THEN j.expected_count ELSE 0 END,j.created_at,j.started_at,j.finished_at,j.effective_at,j.error_code,COALESCE(j.error_detail->>'message',j.error_detail::text,''),j.completed_notice_at IS NOT NULL,
 COALESCE((SELECT result->>'mode' FROM org.change_operation u WHERE u.undo_of=j.operation_id AND u.state='succeeded'),'') FROM org.asset_assignment_job j`

func scanAssignmentJob(row interface{ Scan(...any) error }) (AssignmentJob, error) {
	var j AssignmentJob
	err := row.Scan(&j.ID, &j.OperationID, &j.State, &j.Total, &j.Processed, &j.Assigned, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &j.EffectiveAt, &j.ErrorCode, &j.ErrorDetail, &j.Acknowledged, &j.UndoMode)
	return j, err
}
func (s *Store) AssignmentJobs(ctx context.Context) ([]AssignmentJob, error) {
	rows, err := s.DB.QueryContext(ctx, assignmentJobsSQL+" ORDER BY j.created_at DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssignmentJob{}
	for rows.Next() {
		j, e := scanAssignmentJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Store) AssignmentJob(ctx context.Context, id string) (AssignmentJob, error) {
	return scanAssignmentJob(s.DB.QueryRowContext(ctx, assignmentJobsSQL+" WHERE j.id=$1", id))
}
func (s *Store) AcknowledgeAssignment(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE org.asset_assignment_job SET completed_notice_at=COALESCE(completed_notice_at,clock_timestamp()) WHERE id=$1 AND state IN ('succeeded','failed')", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return bad("id", "任务尚未完成或不存在")
	}
	return nil
}
