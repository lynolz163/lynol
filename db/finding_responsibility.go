package db

import (
	"context"
	"fmt"
	"strconv"
)

type FindingResponsibilityOptions struct {
	BusinessLines []string `json:"business_lines"`
	Assignees     []string `json:"assignees"`
}

func (d *DB) FindingResponsibilityOptions(ctx context.Context) (FindingResponsibilityOptions, error) {
	out := FindingResponsibilityOptions{BusinessLines: []string{}, Assignees: []string{}}
	rows, err := d.QueryContext(ctx, `SELECT 'business_line',name FROM business_lines UNION SELECT 'assignee',name FROM responsibility_parties ORDER BY 1,2`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			return out, err
		}
		if kind == "business_line" {
			out.BusinessLines = append(out.BusinessLines, name)
		} else {
			out.Assignees = append(out.Assignees, name)
		}
	}
	return out, rows.Err()
}

func (d *DB) SetFindingResponsibility(ctx context.Context, ids []int64, businessLine, assignee string) ([]string, error) {
	var valid bool
	if err := d.QueryRowContext(ctx, `SELECT
 ($1='' OR EXISTS(SELECT 1 FROM business_lines WHERE name=$1 AND active)) AND
 ($2='' OR EXISTS(SELECT 1 FROM responsibility_parties WHERE name=$2 AND active))`, businessLine, assignee).Scan(&valid); err != nil {
		return nil, err
	}
	if !valid {
		return nil, fmt.Errorf("%w: 请从配置中选择已启用的业务线和负责人", ErrResponsibilityInput)
	}
	rows, err := d.QueryContext(ctx, `UPDATE findings SET business_line=$2,assignee=$3,
 business_line_id=(SELECT id FROM business_lines WHERE name=$2),
 assignee_id=(SELECT id FROM responsibility_parties WHERE name=$3),
 responsibility_sla=(SELECT sla FROM business_lines WHERE name=$2),assignment_source='manual',assignment_conflict=false
 WHERE id=ANY($1::bigint[]) RETURNING id`, ids, businessLine, assignee)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, strconv.FormatInt(id, 10))
	}
	return out, rows.Err()
}
