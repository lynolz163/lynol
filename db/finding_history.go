package db

import (
	"context"
	"encoding/json"
	"time"
)

type FindingHistoryEvent struct {
	ID         int64           `json:"id"`
	FindingID  int64           `json:"finding_id,string"`
	Kind       string          `json:"kind"`
	OldStatus  string          `json:"old_status"`
	NewStatus  string          `json:"new_status"`
	Actor      string          `json:"actor"`
	OccurredAt time.Time       `json:"occurred_at"`
	Source     string          `json:"source"`
	Metadata   json.RawMessage `json:"metadata"`
}

func (d *DB) FindingHistory(ctx context.Context, findingID, before int64) ([]FindingHistoryEvent, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,finding_id,kind,old_status,new_status,actor,occurred_at,source,metadata
		FROM finding_history WHERE finding_id=$1 AND ($2::bigint=0 OR
		(occurred_at,id)<(SELECT occurred_at,id FROM finding_history WHERE id=$2 AND finding_id=$1))
		ORDER BY occurred_at DESC,id DESC LIMIT 50`, findingID, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []FindingHistoryEvent{}
	for rows.Next() {
		var event FindingHistoryEvent
		if err := rows.Scan(&event.ID, &event.FindingID, &event.Kind, &event.OldStatus, &event.NewStatus,
			&event.Actor, &event.OccurredAt, &event.Source, &event.Metadata); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// A new remediation attempt is allowed after a failed retest or explicit reopen.
// A repeated click within the same attempt keeps its recorded timestamp.
const findingNewAttempt = `EXISTS (SELECT 1 FROM finding_history h WHERE h.finding_id=f.id
	AND h.occurred_at > %s AND (
	  (h.kind='status_changed' AND h.old_status IN ('fixed','resolved')
	   AND h.new_status NOT IN ('fixed','resolved'))
	  OR (h.kind='retest_completed' AND h.metadata->>'status'='completed'
	      AND h.metadata->>'verdict'='reproduced')))`
