package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/Autumn-27/artex/db"
)

type label struct {
	Name   string
	Active bool
}
type labels map[string]map[int64]label

func (s *Store) labels(ctx context.Context) (labels, error) {
	out := labels{"department": {}, "business_line": {}, "assignee": {}}
	rows, err := s.DB.QueryContext(ctx, `SELECT 'department',id,name,active FROM org.department UNION ALL SELECT 'business_line',id,name,active FROM org.business_line UNION ALL SELECT 'assignee',id,name,active FROM org.app_user`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var id int64
		var v label
		if err = rows.Scan(&kind, &id, &v.Name, &v.Active); err != nil {
			return nil, err
		}
		out[kind][id] = v
	}
	return out, rows.Err()
}
func (l labels) name(kind string, id *int64) string {
	if id == nil {
		return ""
	}
	return l[kind][*id].Name
}
func (s *Store) Metrics(ctx context.Context, from, to string, f db.FindingMetricsFilter) (db.FindingMetricsReport, error) {
	if err := ValidateFilter(f); err != nil {
		return db.FindingMetricsReport{}, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id,s.day::text,s.generated_at,s.history_since,s.stock_known,s.quality_known,s.sla_days
 FROM report.daily_snapshot_head h JOIN report.daily_snapshot s ON s.id=h.snapshot_id WHERE h.day BETWEEN $1::date AND $2::date ORDER BY h.day`, from, to)
	if err != nil {
		return db.FindingMetricsReport{}, err
	}
	days := map[string]*daily{}
	snapshotIDs := []int64{}
	for rows.Next() {
		d := &daily{Buckets: map[string]*bucket{}}
		var since sql.NullTime
		var raw []byte
		if err = rows.Scan(&d.ID, &d.Day, &d.At, &since, &d.StockKnown, &d.QualityKnown, &raw); err != nil {
			rows.Close()
			return db.FindingMetricsReport{}, err
		}
		d.HistorySince = since.Time
		if err = json.Unmarshal(raw, &d.SLA); err != nil {
			rows.Close()
			return db.FindingMetricsReport{}, err
		}
		days[d.Day] = d
		snapshotIDs = append(snapshotIDs, d.ID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return db.FindingMetricsReport{}, err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT h.day::text,v.department_id,v.business_line_id,v.owner_id,v.severity,v.vulnclass,v.new_count,v.closed_count,v.reopened_count,v.open_count,v.overdue_count,v.sla_closed_count,v.sla_met_count,v.retest_passed_count,v.retest_failed_count,v.retest_other_count,v.first_fix_passed_count,v.first_fix_total_count,v.verified_fix_count,v.rework_round_count,v.age_0_7_count,v.age_7_30_count,v.age_30_plus_count,v.duration_samples,v.overdue_items,v.unknown_event_count,v.unknown_open_count,v.missing_sla_closed_count,v.missing_sla_open_count,v.missing_discovery_count
 FROM report.daily_snapshot h JOIN report.daily_vuln_stat v ON v.snapshot_id=h.id WHERE h.id=ANY($1::bigint[]) ORDER BY h.day,v.id`, snapshotIDs)
	if err != nil {
		return db.FindingMetricsReport{}, err
	}
	for rows.Next() {
		var day string
		b := &bucket{}
		var open, overdue, a0, a1, a2 sql.NullInt64
		var samples, items []byte
		if err = rows.Scan(&day, &b.dimension.DepartmentID, &b.LineID, &b.dimension.OwnerID, &b.dimension.Severity, &b.Class, &b.New, &b.Closed, &b.Reopened, &open, &overdue, &b.SLAClosed, &b.SLAMet, &b.RetestPassed, &b.RetestFailed, &b.RetestOther, &b.FirstRetestPassed, &b.FirstRetestTotal, &b.VerifiedFixes, &b.ReworkRounds, &a0, &a1, &a2, &samples, &items, &b.UnknownEvents, &b.UnknownOpen, &b.MissingSLAClosed, &b.MissingSLAOpen, &b.MissingDiscovery); err != nil {
			rows.Close()
			return db.FindingMetricsReport{}, err
		}
		b.Open = int(open.Int64)
		b.Overdue = int(overdue.Int64)
		b.Ages = [3]int{int(a0.Int64), int(a1.Int64), int(a2.Int64)}
		b.FindingMetricBucket.DepartmentID = b.dimension.DepartmentID
		b.BusinessLineID = b.LineID
		b.FindingMetricBucket.OwnerID = b.dimension.OwnerID
		b.FindingMetricBucket.Severity = b.dimension.Severity
		b.VulnClass = b.Class
		if err = json.Unmarshal(samples, &b.Samples); err != nil {
			rows.Close()
			return db.FindingMetricsReport{}, err
		}
		if err = json.Unmarshal(items, &b.OverdueItems); err != nil {
			rows.Close()
			return db.FindingMetricsReport{}, err
		}
		days[day].Buckets[b.dimension.key()] = b
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return db.FindingMetricsReport{}, err
	}
	names, err := s.labels(ctx)
	if err != nil {
		return db.FindingMetricsReport{}, err
	}
	return summarize(from, to, days, f, names), nil
}

func summarize(from, to string, days map[string]*daily, f db.FindingMetricsFilter, names labels) db.FindingMetricsReport {
	r := db.FindingMetricsReport{AttributionVersion: 1, Trend: []db.FindingMetricTrend{}, Durations: []db.FindingDuration{}, Distribution: []*db.FindingMetricBucket{}, Groups: []db.FindingMetricGroup{}, Overdue: []db.FindingOverdue{}, SnapshotIDs: []int64{}, QualityKnown: true, FilterOptions: db.FindingMetricOptions{BusinessLineIDs: []int64{}, OwnerIDs: []int64{}}}
	distribution := map[string]*db.FindingMetricBucket{}
	samples := map[string][]float64{}
	lineIDs := map[int64]bool{}
	ownerIDs := map[int64]bool{}
	begin, _ := time.ParseInLocation("2006-01-02", from, db.FindingMetricsLocation)
	end, _ := time.ParseInLocation("2006-01-02", to, db.FindingMetricsLocation)
	for day := begin; !day.After(end); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		d := days[key]
		trend := db.FindingMetricTrend{Day: key}
		if d == nil {
			r.DataQuality.MissingSnapshotDays++
			r.Trend = append(r.Trend, trend)
			continue
		}
		r.SnapshotIDs = append(r.SnapshotIDs, d.ID)
		r.QualityKnown = r.QualityKnown && d.QualityKnown
		if !d.StockKnown {
			r.DataQuality.MissingSnapshotDays++
		}
		open, overdue := 0, 0
		for k, b := range d.Buckets {
			if filterID(f.DepartmentID, b.dimension.DepartmentID) {
				if b.LineID != nil {
					lineIDs[*b.LineID] = true
				}
				if filterID(f.BusinessLineID, b.LineID) && b.dimension.OwnerID != nil {
					ownerIDs[*b.dimension.OwnerID] = true
				}
			}
			if !filterBucket(f, b) {
				continue
			}
			trend.New += b.New
			trend.Closed += b.Closed
			trend.Reopened += b.Reopened
			open += b.Open
			overdue += b.Overdue
			r.Summary.New += b.New
			r.Summary.Closed += b.Closed
			r.Summary.Reopened += b.Reopened
			r.Summary.SLAClosed += b.SLAClosed
			r.Summary.SLAMet += b.SLAMet
			r.Summary.RetestPassed += b.RetestPassed
			r.Summary.RetestFailed += b.RetestFailed
			r.Summary.RetestOther += b.RetestOther
			r.Summary.FirstRetestPassed += b.FirstRetestPassed
			r.Summary.FirstRetestTotal += b.FirstRetestTotal
			r.Summary.VerifiedFixes += b.VerifiedFixes
			r.Summary.ReworkRounds += b.ReworkRounds
			r.DataQuality.UnknownEvents += b.UnknownEvents
			r.DataQuality.MissingSLA += b.MissingSLAClosed
			r.DataQuality.MissingDiscovery += b.MissingDiscovery
			if distribution[k] == nil {
				distribution[k] = &db.FindingMetricBucket{DepartmentID: b.dimension.DepartmentID, BusinessLineID: b.LineID, OwnerID: b.dimension.OwnerID, Department: names.name("department", b.dimension.DepartmentID), BusinessLine: names.name("business_line", b.LineID), Assignee: names.name("assignee", b.dimension.OwnerID), Severity: b.dimension.Severity, VulnClass: b.Class, Samples: map[string][]float64{}}
			}
			out := distribution[k]
			out.New += b.New
			out.Closed += b.Closed
			out.SLAClosed += b.SLAClosed
			out.SLAMet += b.SLAMet
			for stage, values := range b.Samples {
				samples[stage] = append(samples[stage], values...)
				out.Samples[stage] = append(out.Samples[stage], values...)
			}
			if key == to && d.StockKnown {
				out.Open = b.Open
				out.Overdue = b.Overdue
				out.Ages = b.Ages
				r.Summary.Open += b.Open
				r.Summary.Overdue += b.Overdue
				for i, n := range b.Ages {
					r.Summary.Ages[i] += n
				}
				r.DataQuality.UnknownOpen += b.UnknownOpen
				r.DataQuality.MissingSLA += b.MissingSLAOpen
				for _, item := range b.OverdueItems {
					item.Department = names.name("department", item.DepartmentID)
					item.BusinessLine = names.name("business_line", item.BusinessLineID)
					item.Assignee = names.name("assignee", item.OwnerID)
					r.Overdue = append(r.Overdue, item)
				}
			}
		}
		if d.StockKnown {
			trend.Open = &open
			trend.Overdue = &overdue
		}
		r.Trend = append(r.Trend, trend)
		if key == to {
			r.StockKnown = d.StockKnown
			r.GeneratedAt = &d.At
			if !d.HistorySince.IsZero() {
				r.HistorySince = &d.HistorySince
			}
			r.SLA = d.SLA
		}
	}
	r.DataQuality.UnknownAttribution = r.DataQuality.UnknownEvents + r.DataQuality.UnknownOpen
	for id := range lineIDs {
		r.FilterOptions.BusinessLineIDs = append(r.FilterOptions.BusinessLineIDs, id)
	}
	for id := range ownerIDs {
		r.FilterOptions.OwnerIDs = append(r.FilterOptions.OwnerIDs, id)
	}
	sort.Slice(r.FilterOptions.BusinessLineIDs, func(i, j int) bool { return r.FilterOptions.BusinessLineIDs[i] < r.FilterOptions.BusinessLineIDs[j] })
	sort.Slice(r.FilterOptions.OwnerIDs, func(i, j int) bool { return r.FilterOptions.OwnerIDs[i] < r.FilterOptions.OwnerIDs[j] })
	for _, kind := range []string{"department", "business_line", "assignee"} {
		groups := map[string]*db.FindingMetricGroup{}
		times := map[string][]float64{}
		for _, b := range distribution {
			id := b.DepartmentID
			if kind == "business_line" {
				id = b.BusinessLineID
			} else if kind == "assignee" {
				id = b.OwnerID
			}
			k := idKey(id)
			if groups[k] == nil {
				g := &db.FindingMetricGroup{ID: id, Dimension: kind, Name: names.name(kind, id), Active: true}
				if id != nil {
					g.Active = names[kind][*id].Active
				}
				groups[k] = g
			}
			g := groups[k]
			g.Open += b.Open
			g.Overdue += b.Overdue
			g.Closed += b.Closed
			g.SLAClosed += b.SLAClosed
			g.SLAMet += b.SLAMet
			times[k] = append(times[k], b.Samples["closure"]...)
		}
		for k, g := range groups {
			duration := duration("closure", times[k])
			g.Average = duration.Average
			g.P90 = duration.P90
			r.Groups = append(r.Groups, *g)
		}
	}
	for _, b := range distribution {
		b.Samples = nil
		r.Distribution = append(r.Distribution, b)
	}
	sort.Slice(r.Groups, func(i, j int) bool {
		a, b := r.Groups[i], r.Groups[j]
		if a.Dimension != b.Dimension {
			return a.Dimension < b.Dimension
		}
		if a.Overdue != b.Overdue {
			return a.Overdue > b.Overdue
		}
		return a.Name < b.Name
	})
	sort.Slice(r.Distribution, func(i, j int) bool {
		a, b := r.Distribution[i], r.Distribution[j]
		return a.Severity+a.VulnClass+idKey(a.DepartmentID)+idKey(a.BusinessLineID)+idKey(a.OwnerID) < b.Severity+b.VulnClass+idKey(b.DepartmentID)+idKey(b.BusinessLineID)+idKey(b.OwnerID)
	})
	sort.Slice(r.Overdue, func(i, j int) bool { return r.Overdue[i].OverdueDays > r.Overdue[j].OverdueDays })
	for _, stage := range []string{"dispatch", "development", "waiting", "verification", "closure", "rework"} {
		r.Durations = append(r.Durations, duration(stage, samples[stage]))
	}
	return r
}
func duration(key string, values []float64) db.FindingDuration {
	r := db.FindingDuration{Key: key, Count: len(values)}
	if len(values) > 0 {
		sort.Float64s(values)
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		avg := sum / float64(len(values))
		p90 := values[int(math.Ceil(.9*float64(len(values))))-1]
		r.Average = &avg
		r.P90 = &p90
	}
	return r
}
