package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

var FindingMetricsLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type FindingSLA map[string]int

type FindingMetricsFilter struct {
	AttributionVersion int    `json:"attribution_version,omitempty"`
	DepartmentID       *int64 `json:"department_id,omitempty"`
	BusinessLineID     *int64 `json:"business_line_id,omitempty"`
	OwnerID            *int64 `json:"owner_id,omitempty"`
	Severity           string `json:"severity"`
	BusinessLine       string `json:"business_line"`
	Assignee           string `json:"assignee"`
}

func (f FindingMetricsFilter) matches(severity, businessLine, assignee string) bool {
	return (f.Severity == "" || f.Severity == severity) &&
		(f.BusinessLine == "" || f.BusinessLine == businessLine || (f.BusinessLine == "__none__" && businessLine == "")) &&
		(f.Assignee == "" || f.Assignee == assignee || (f.Assignee == "__none__" && assignee == ""))
}

type FindingMetricGroup struct {
	ID        *int64   `json:"id"`
	Active    bool     `json:"active"`
	Dimension string   `json:"dimension"`
	Name      string   `json:"name"`
	Open      int      `json:"open"`
	Overdue   int      `json:"overdue"`
	Closed    int      `json:"closed"`
	SLAMet    int      `json:"sla_met"`
	SLAClosed int      `json:"sla_closed"`
	Average   *float64 `json:"average_hours"`
	P90       *float64 `json:"p90_hours"`
}

type FindingMetricBucket struct {
	DepartmentID      *int64               `json:"department_id"`
	BusinessLineID    *int64               `json:"business_line_id"`
	OwnerID           *int64               `json:"owner_id"`
	Department        string               `json:"department"`
	BusinessLine      string               `json:"business_line"`
	Assignee          string               `json:"assignee"`
	Severity          string               `json:"severity"`
	VulnClass         string               `json:"vulnclass"`
	New               int                  `json:"new"`
	Closed            int                  `json:"closed"`
	Reopened          int                  `json:"reopened"`
	Open              int                  `json:"open"`
	Overdue           int                  `json:"overdue"`
	SLAMet            int                  `json:"sla_met"`
	SLAClosed         int                  `json:"sla_closed"`
	RetestPassed      int                  `json:"retest_passed"`
	RetestFailed      int                  `json:"retest_failed"`
	RetestOther       int                  `json:"retest_other"`
	FirstRetestPassed int                  `json:"first_retest_passed"`
	FirstRetestTotal  int                  `json:"first_retest_total"`
	VerifiedFixes     int                  `json:"verified_fixes"`
	ReworkRounds      int                  `json:"rework_rounds"`
	Ages              [3]int               `json:"ages"`
	Samples           map[string][]float64 `json:"samples,omitempty"`
}

type FindingOverdue struct {
	DepartmentID   *int64     `json:"department_id"`
	BusinessLineID *int64     `json:"business_line_id"`
	OwnerID        *int64     `json:"owner_id"`
	Department     string     `json:"department"`
	AssetID        *int64     `json:"asset_id"`
	Asset          string     `json:"asset"`
	DiscoveredAt   *time.Time `json:"discovered_at"`
	DeadlineAt     *time.Time `json:"deadline_at"`
	AsOf           *time.Time `json:"as_of"`
	BusinessLine   string     `json:"business_line"`
	Assignee       string     `json:"assignee"`
	ID             int64      `json:"id,string"`
	Name           string     `json:"name"`
	Severity       string     `json:"severity"`
	AgeDays        float64    `json:"age_days"`
	OverdueDays    float64    `json:"overdue_days"`
	Available      bool       `json:"available"`
}

type FindingDailySnapshot struct {
	Day          string                 `json:"day"`
	GeneratedAt  time.Time              `json:"generated_at"`
	HistorySince time.Time              `json:"history_since"`
	StockKnown   bool                   `json:"stock_known"`
	QualityKnown bool                   `json:"quality_known"`
	SLA          FindingSLA             `json:"sla"`
	Buckets      []*FindingMetricBucket `json:"buckets"`
	Overdue      []FindingOverdue       `json:"overdue"`
}

type FindingDuration struct {
	Key     string   `json:"key"`
	Count   int      `json:"count"`
	Average *float64 `json:"average_hours"`
	P90     *float64 `json:"p90_hours"`
}

type FindingMetricTrend struct {
	Day      string `json:"day"`
	New      int    `json:"new"`
	Closed   int    `json:"closed"`
	Reopened int    `json:"reopened"`
	Open     *int   `json:"open"`
	Overdue  *int   `json:"overdue"`
}

type FindingMetricsReport struct {
	AttributionVersion int                    `json:"attribution_version,omitempty"`
	SnapshotIDs        []int64                `json:"snapshot_ids,omitempty"`
	DataQuality        FindingMetricsQuality  `json:"data_quality"`
	FilterOptions      FindingMetricOptions   `json:"filter_options"`
	HasCustomSLA       bool                   `json:"has_custom_sla"`
	Groups             []FindingMetricGroup   `json:"groups"`
	GeneratedAt        *time.Time             `json:"generated_at"`
	HistorySince       *time.Time             `json:"history_since"`
	StockKnown         bool                   `json:"stock_known"`
	QualityKnown       bool                   `json:"quality_known"`
	Summary            FindingMetricBucket    `json:"summary"`
	Trend              []FindingMetricTrend   `json:"trend"`
	Durations          []FindingDuration      `json:"durations"`
	Distribution       []*FindingMetricBucket `json:"distribution"`
	Overdue            []FindingOverdue       `json:"overdue"`
	SLA                FindingSLA             `json:"sla"`
}

type FindingMetricsQuality struct {
	UnknownAttribution  int `json:"unknown_attribution"`
	UnknownEvents       int `json:"unknown_events"`
	UnknownOpen         int `json:"unknown_open"`
	MissingSLA          int `json:"missing_sla"`
	MissingDiscovery    int `json:"missing_discovery"`
	MissingSnapshotDays int `json:"missing_snapshot_days"`
}
type FindingMetricOptions struct {
	BusinessLineIDs []int64 `json:"business_line_ids"`
	OwnerIDs        []int64 `json:"owner_ids"`
}

type findingMetricState struct {
	sla                                  FindingSLA
	id                                   int64
	severity, vulnclass, status          string
	businessLine, assignee               string
	discovered, cycle, dispatched, fixed time.Time
	retest                               time.Time
	firstRetestSeen                      bool
	reworkRounds                         int
	reworkStarted                        time.Time
}

func closedFindingState(status string) bool {
	return status == FindingFixed || status == FindingResolved
}

func activeFindingState(status string) bool {
	return status == FindingPending || status == FindingInProgress || status == FindingConfirmed
}

func (s *findingMetricState) bucket(buckets map[string]*FindingMetricBucket) *FindingMetricBucket {
	key := s.severity + "\x00" + s.vulnclass + "\x00" + s.businessLine + "\x00" + s.assignee
	bucket := buckets[key]
	if bucket == nil {
		bucket = &FindingMetricBucket{Severity: s.severity, VulnClass: s.vulnclass, BusinessLine: s.businessLine, Assignee: s.assignee, Samples: map[string][]float64{}}
		buckets[key] = bucket
	}
	return bucket
}

func (b *FindingMetricBucket) duration(key string, from, to time.Time) {
	if !from.IsZero() && !to.Before(from) {
		b.Samples[key] = append(b.Samples[key], to.Sub(from).Hours())
	}
}

func replayFindingEvent(states map[int64]*findingMetricState, event FindingHistoryEvent, buckets map[string]*FindingMetricBucket, sla FindingSLA) error {
	var metadata struct {
		Severity     string     `json:"severity"`
		BusinessLine string     `json:"business_line"`
		Assignee     string     `json:"assignee"`
		VulnClass    string     `json:"vulnclass"`
		Status       string     `json:"status"`
		Verdict      string     `json:"verdict"`
		RequestedAt  time.Time  `json:"requested_at"`
		SLA          FindingSLA `json:"sla"`
	}
	if err := json.Unmarshal(event.Metadata, &metadata); err != nil {
		return err
	}
	state := states[event.FindingID]
	if event.Kind == "discovered" {
		state = &findingMetricState{sla: metadata.SLA, id: event.FindingID, severity: metadata.Severity, vulnclass: metadata.VulnClass,
			businessLine: metadata.BusinessLine, assignee: metadata.Assignee, discovered: event.OccurredAt, cycle: event.OccurredAt, status: event.NewStatus}
		states[event.FindingID] = state
		state.bucket(buckets).New++
		return nil
	}
	if state == nil {
		return nil // An imported journal can contain an event without a known discovery.
	}
	if event.Kind == "classification_changed" || event.Kind == "baseline" {
		state.severity, state.vulnclass = metadata.Severity, metadata.VulnClass
	}
	if event.Kind == "assignment_changed" || event.Kind == "baseline" {
		state.businessLine, state.assignee = metadata.BusinessLine, metadata.Assignee
		state.sla = metadata.SLA
	}
	if event.Kind == "sla_changed" {
		state.sla = metadata.SLA
	}
	bucket := state.bucket(buckets)
	nextStatus := state.status
	switch event.Kind {
	case "baseline":
		state.status = event.NewStatus
		return nil // Current observation has no known transition time.
	case "removed":
		state.status = "removed"
		return nil
	case "dispatched":
		bucket.duration("dispatch", state.cycle, event.OccurredAt)
		state.dispatched = event.OccurredAt
	case "developer_fixed":
		bucket.duration("development", state.dispatched, event.OccurredAt)
		state.fixed = event.OccurredAt
	case "retest_requested":
		bucket.duration("waiting", state.fixed, event.OccurredAt)
		state.retest = event.OccurredAt
	case "retest_completed":
		if metadata.Status != "completed" {
			bucket.RetestOther++
			break
		}
		if !state.firstRetestSeen && (metadata.Verdict == "fixed" || metadata.Verdict == "reproduced") {
			state.firstRetestSeen = true
			bucket.FirstRetestTotal++
			if metadata.Verdict == "fixed" {
				bucket.FirstRetestPassed++
			}
		}
		switch metadata.Verdict {
		case "fixed":
			bucket.RetestPassed++
			if !closedFindingState(state.status) {
				bucket.VerifiedFixes++
				bucket.ReworkRounds += state.reworkRounds
				bucket.duration("rework", state.reworkStarted, event.OccurredAt)
			}
			bucket.duration("verification", metadata.RequestedAt, event.OccurredAt)
			nextStatus = FindingFixed
		case "reproduced":
			bucket.RetestFailed++
			state.reworkRounds++
			if state.reworkStarted.IsZero() {
				state.reworkStarted = event.OccurredAt
			}
			if closedFindingState(state.status) {
				nextStatus = FindingConfirmed
			}
			state.cycle = event.OccurredAt
			state.dispatched, state.fixed, state.retest = time.Time{}, time.Time{}, time.Time{}
		default:
			bucket.RetestOther++
		}
	case "status_changed":
		nextStatus = event.NewStatus
	}
	if closedFindingState(nextStatus) && !closedFindingState(state.status) {
		bucket.Closed++
		bucket.duration("closure", state.discovered, event.OccurredAt)
		effectiveSLA := sla
		if state.sla != nil {
			effectiveSLA = state.sla
		}
		if days := effectiveSLA[state.severity]; days > 0 && !event.OccurredAt.Before(state.discovered) {
			bucket.SLAClosed++
			if !event.OccurredAt.After(state.discovered.Add(time.Duration(days) * 24 * time.Hour)) {
				bucket.SLAMet++
			}
		}
		state.reworkRounds, state.reworkStarted = 0, time.Time{}
	}
	if activeFindingState(nextStatus) && closedFindingState(state.status) {
		bucket.Reopened++
		if event.Kind != "retest_completed" {
			state.reworkRounds, state.reworkStarted = 0, time.Time{}
		}
		state.cycle = event.OccurredAt
		state.dispatched, state.fixed, state.retest = time.Time{}, time.Time{}, time.Time{}
	}
	state.status = nextStatus
	return nil
}

// BuildFindingDailySnapshots replays the journal once. Stock before history
// collection began is deliberately unknown; known backfilled events still count.
func BuildFindingDailySnapshots(events []FindingHistoryEvent, names map[int64]string, now, historySince time.Time, sla FindingSLA, historicalSLA map[string]FindingSLA) ([]FindingDailySnapshot, error) {
	local := now.In(FindingMetricsLocation)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, FindingMetricsLocation)
	start := today.AddDate(0, 0, -365)
	states := map[int64]*findingMetricState{}
	cursor := 0
	past := map[string]*FindingMetricBucket{}
	for cursor < len(events) && events[cursor].OccurredAt.Before(start) {
		if err := replayFindingEvent(states, events[cursor], past, sla); err != nil {
			return nil, err
		}
		cursor++
	}
	result := make([]FindingDailySnapshot, 0, 366)
	for day := start; !day.After(today); day = day.AddDate(0, 0, 1) {
		daySLA := sla
		if policy, ok := historicalSLA[day.Format("2006-01-02")]; ok {
			daySLA = policy
		}
		boundary := day.AddDate(0, 0, 1)
		asOf := boundary.Add(-time.Nanosecond)
		if asOf.After(now) {
			asOf = now
		}
		buckets := map[string]*FindingMetricBucket{}
		for cursor < len(events) && events[cursor].OccurredAt.Before(boundary) && !events[cursor].OccurredAt.After(now) {
			if err := replayFindingEvent(states, events[cursor], buckets, daySLA); err != nil {
				return nil, err
			}
			cursor++
		}
		snapshot := FindingDailySnapshot{Day: day.Format("2006-01-02"), GeneratedAt: now, HistorySince: historySince,
			StockKnown: !asOf.Before(historySince), SLA: daySLA, Buckets: []*FindingMetricBucket{}, Overdue: []FindingOverdue{}}
		for _, state := range states {
			if !activeFindingState(state.status) || !snapshot.StockKnown {
				continue
			}
			bucket := state.bucket(buckets)
			bucket.Open++
			age := asOf.Sub(state.discovered).Hours() / 24
			switch {
			case age < 7:
				bucket.Ages[0]++
			case age < 30:
				bucket.Ages[1]++
			default:
				bucket.Ages[2]++
			}
			days := daySLA[state.severity]
			if state.sla != nil {
				days = state.sla[state.severity]
			}
			if days > 0 && age > float64(days) {
				bucket.Overdue++
				name, available := names[state.id]
				if name == "" {
					name = fmt.Sprintf("漏洞 #%d", state.id)
				}
				snapshot.Overdue = append(snapshot.Overdue, FindingOverdue{
					ID: state.id, Name: name, Severity: state.severity, BusinessLine: state.businessLine, Assignee: state.assignee, AgeDays: age,
					OverdueDays: age - float64(days), Available: available,
				})
			}
		}
		for _, bucket := range buckets {
			snapshot.Buckets = append(snapshot.Buckets, bucket)
		}
		sort.Slice(snapshot.Buckets, func(i, j int) bool {
			a, b := snapshot.Buckets[i], snapshot.Buckets[j]
			return a.Severity+"\x00"+a.VulnClass < b.Severity+"\x00"+b.VulnClass
		})
		sort.Slice(snapshot.Overdue, func(i, j int) bool {
			if snapshot.Overdue[i].OverdueDays == snapshot.Overdue[j].OverdueDays {
				return snapshot.Overdue[i].ID < snapshot.Overdue[j].ID
			}
			return snapshot.Overdue[i].OverdueDays > snapshot.Overdue[j].OverdueDays
		})
		snapshot.QualityKnown = true
		result = append(result, snapshot)
	}
	return result, nil
}

func (d *DB) RefreshFindingMetrics(ctx context.Context) error {
	tx, err := d.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rawSLA string
	var historySince time.Time
	if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='finding_sla_days'`).Scan(&rawSLA); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT value::timestamptz FROM settings WHERE key='finding_history_started_at'`).Scan(&historySince); err != nil {
		return err
	}
	var sla FindingSLA
	if err := json.Unmarshal([]byte(rawSLA), &sla); err != nil {
		return err
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT now()`).Scan(&now); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,finding_id,kind,old_status,new_status,actor,occurred_at,source,metadata
		FROM finding_history WHERE occurred_at<=$1 ORDER BY occurred_at,id`, now)
	if err != nil {
		return err
	}
	var events []FindingHistoryEvent
	for rows.Next() {
		var event FindingHistoryEvent
		if err := rows.Scan(&event.ID, &event.FindingID, &event.Kind, &event.OldStatus, &event.NewStatus,
			&event.Actor, &event.OccurredAt, &event.Source, &event.Metadata); err != nil {
			rows.Close()
			return err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT id,COALESCE(NULLIF(name,''),NULLIF(vulnclass,''),'未命名漏洞') FROM findings`)
	if err != nil {
		return err
	}
	names := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return err
		}
		names[id] = name
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	// Late-entered milestones change phase durations on past days. Rebuild them
	// with their original SLA policy rather than applying today's rules to history.
	today := now.In(FindingMetricsLocation).Format("2006-01-02")
	rows, err = tx.QueryContext(ctx, `SELECT day::text,payload->'sla' FROM daily_vuln_stats WHERE day<$1::date`, today)
	if err != nil {
		return err
	}
	historicalSLA := map[string]FindingSLA{}
	for rows.Next() {
		var day string
		var raw []byte
		var policy FindingSLA
		if err := rows.Scan(&day, &raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &policy); err != nil {
			rows.Close()
			return err
		}
		historicalSLA[day] = policy
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	snapshots, err := BuildFindingDailySnapshots(events, names, now, historySince, sla, historicalSLA)
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		payload, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO daily_vuln_stats(day,generated_at,payload) VALUES ($1,$2,$3)
			ON CONFLICT(day) DO UPDATE SET generated_at=excluded.generated_at,payload=excluded.payload`,
			snapshot.Day, now, payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func SummarizeFindingMetrics(snapshots []FindingDailySnapshot, filter FindingMetricsFilter) FindingMetricsReport {
	report := FindingMetricsReport{Trend: []FindingMetricTrend{}, Durations: []FindingDuration{},
		Distribution: []*FindingMetricBucket{}, Overdue: []FindingOverdue{}, Groups: []FindingMetricGroup{}, QualityKnown: len(snapshots) > 0}
	samples := map[string][]float64{}
	distribution := map[string]*FindingMetricBucket{}
	for _, snapshot := range snapshots {
		report.QualityKnown = report.QualityKnown && snapshot.QualityKnown
		trend := FindingMetricTrend{Day: snapshot.Day}
		var open, overdue int
		for _, bucket := range snapshot.Buckets {
			if !filter.matches(bucket.Severity, bucket.BusinessLine, bucket.Assignee) {
				continue
			}
			trend.New += bucket.New
			trend.Closed += bucket.Closed
			trend.Reopened += bucket.Reopened
			open += bucket.Open
			overdue += bucket.Overdue
			report.Summary.New += bucket.New
			report.Summary.Closed += bucket.Closed
			report.Summary.Reopened += bucket.Reopened
			report.Summary.SLAMet += bucket.SLAMet
			report.Summary.SLAClosed += bucket.SLAClosed
			report.Summary.RetestPassed += bucket.RetestPassed
			report.Summary.RetestFailed += bucket.RetestFailed
			report.Summary.RetestOther += bucket.RetestOther
			report.Summary.FirstRetestPassed += bucket.FirstRetestPassed
			report.Summary.FirstRetestTotal += bucket.FirstRetestTotal
			report.Summary.VerifiedFixes += bucket.VerifiedFixes
			report.Summary.ReworkRounds += bucket.ReworkRounds
			key := bucket.Severity + "\x00" + bucket.VulnClass + "\x00" + bucket.BusinessLine + "\x00" + bucket.Assignee
			if distribution[key] == nil {
				distribution[key] = &FindingMetricBucket{Severity: bucket.Severity, VulnClass: bucket.VulnClass, BusinessLine: bucket.BusinessLine, Assignee: bucket.Assignee, Samples: map[string][]float64{}}
			}
			item := distribution[key]
			item.New += bucket.New
			item.Closed += bucket.Closed
			item.SLAMet += bucket.SLAMet
			item.SLAClosed += bucket.SLAClosed
			item.Samples["closure"] = append(item.Samples["closure"], bucket.Samples["closure"]...)
			for key, values := range bucket.Samples {
				samples[key] = append(samples[key], values...)
			}
		}
		if snapshot.StockKnown {
			trend.Open, trend.Overdue = &open, &overdue
		}
		report.Trend = append(report.Trend, trend)
	}
	if len(snapshots) > 0 {
		last := snapshots[len(snapshots)-1]
		report.GeneratedAt, report.HistorySince = &last.GeneratedAt, &last.HistorySince
		report.StockKnown, report.SLA = last.StockKnown, last.SLA
		for _, bucket := range last.Buckets {
			if !filter.matches(bucket.Severity, bucket.BusinessLine, bucket.Assignee) {
				continue
			}
			item := distribution[bucket.Severity+"\x00"+bucket.VulnClass+"\x00"+bucket.BusinessLine+"\x00"+bucket.Assignee]
			item.Open, item.Overdue, item.Ages = bucket.Open, bucket.Overdue, bucket.Ages
			report.Summary.Open += bucket.Open
			report.Summary.Overdue += bucket.Overdue
			for i, count := range bucket.Ages {
				report.Summary.Ages[i] += count
			}
		}
		for _, item := range last.Overdue {
			if filter.matches(item.Severity, item.BusinessLine, item.Assignee) {
				report.Overdue = append(report.Overdue, item)
			}
		}
	}
	for _, dimension := range []string{"business_line", "assignee"} {
		groups := map[string]*FindingMetricGroup{}
		times := map[string][]float64{}
		for _, item := range distribution {
			name := item.BusinessLine
			if dimension == "assignee" {
				name = item.Assignee
			}
			group := groups[name]
			if group == nil {
				group = &FindingMetricGroup{Dimension: dimension, Name: name}
				groups[name] = group
			}
			group.Open += item.Open
			group.Overdue += item.Overdue
			group.Closed += item.Closed
			group.SLAMet += item.SLAMet
			group.SLAClosed += item.SLAClosed
			times[name] = append(times[name], item.Samples["closure"]...)
		}
		for name, group := range groups {
			duration := summarizeFindingDuration("closure", times[name])
			group.Average, group.P90 = duration.Average, duration.P90
			report.Groups = append(report.Groups, *group)
		}
	}
	sort.Slice(report.Groups, func(i, j int) bool {
		a, b := report.Groups[i], report.Groups[j]
		if a.Dimension != b.Dimension {
			return a.Dimension < b.Dimension
		}
		if a.Overdue != b.Overdue {
			return a.Overdue > b.Overdue
		}
		if a.Open != b.Open {
			return a.Open > b.Open
		}
		return a.Name < b.Name
	})
	for _, item := range distribution {
		item.Samples = nil
		report.Distribution = append(report.Distribution, item)
	}
	sort.Slice(report.Distribution, func(i, j int) bool {
		return report.Distribution[i].Severity+"\x00"+report.Distribution[i].VulnClass <
			report.Distribution[j].Severity+"\x00"+report.Distribution[j].VulnClass
	})
	for _, key := range []string{"dispatch", "development", "waiting", "verification", "closure", "rework"} {
		report.Durations = append(report.Durations, summarizeFindingDuration(key, samples[key]))
	}
	return report
}

func summarizeFindingDuration(key string, values []float64) FindingDuration {
	duration := FindingDuration{Key: key, Count: len(values)}
	if len(values) > 0 {
		sort.Float64s(values)
		var sum float64
		for _, value := range values {
			sum += value
		}
		average := sum / float64(len(values))
		p90 := values[int(math.Ceil(float64(len(values))*0.9))-1]
		duration.Average, duration.P90 = &average, &p90
	}
	return duration
}

func (d *DB) FindingMetrics(ctx context.Context, from, to string, filter FindingMetricsFilter) (FindingMetricsReport, error) {
	rows, err := d.QueryContext(ctx, `SELECT payload FROM daily_vuln_stats WHERE day BETWEEN $1::date AND $2::date ORDER BY day`, from, to)
	if err != nil {
		return FindingMetricsReport{}, err
	}
	defer rows.Close()
	snapshots := []FindingDailySnapshot{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return FindingMetricsReport{}, err
		}
		var snapshot FindingDailySnapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return FindingMetricsReport{}, err
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return FindingMetricsReport{}, err
	}
	rows.Close()
	lines, people, err := d.responsibilityAliases(ctx)
	if err != nil {
		return FindingMetricsReport{}, err
	}
	if name, ok := lines[filter.BusinessLine]; ok {
		filter.BusinessLine = name
	}
	if name, ok := people[filter.Assignee]; ok {
		filter.Assignee = name
	}
	for _, snapshot := range snapshots {
		for _, b := range snapshot.Buckets {
			if name, ok := lines[b.BusinessLine]; ok {
				b.BusinessLine = name
			}
			if name, ok := people[b.Assignee]; ok {
				b.Assignee = name
			}
		}
		for i := range snapshot.Overdue {
			b := &snapshot.Overdue[i]
			if name, ok := lines[b.BusinessLine]; ok {
				b.BusinessLine = name
			}
			if name, ok := people[b.Assignee]; ok {
				b.Assignee = name
			}
		}
	}
	report := SummarizeFindingMetrics(snapshots, filter)
	err = d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM business_lines WHERE sla IS NOT NULL AND ($1='' OR name=$1 OR $1=ANY(aliases)))`, filter.BusinessLine).Scan(&report.HasCustomSLA)
	return report, err
}
