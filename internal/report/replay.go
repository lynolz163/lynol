// Package report stores immutable daily generations and reads frozen event attribution.
package report

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Autumn-27/artex/db"
)

type event struct {
	ID, FindingID                          int64
	Kind, OldStatus, NewStatus             string
	At, Recorded                           time.Time
	Metadata                               json.RawMessage
	AssetID, DepartmentID, LineID, OwnerID *int64
	Version                                int
	Quality                                string
	SLADays                                *int
}
type dimension struct {
	DepartmentID, LineID, OwnerID *int64
	Severity, Class               string
}

func (d dimension) key() string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", idKey(d.DepartmentID), idKey(d.LineID), idKey(d.OwnerID), d.Severity, d.Class)
}
func idKey(id *int64) string {
	if id == nil {
		return "0"
	}
	return fmt.Sprint(*id)
}

type bucket struct {
	dimension
	db.FindingMetricBucket
	UnknownEvents, UnknownOpen, MissingSLAClosed, MissingSLAOpen, MissingDiscovery int
	OverdueItems                                                                   []db.FindingOverdue
}
type daily struct {
	ID                       int64
	Day                      string
	At, HistorySince         time.Time
	StockKnown, QualityKnown bool
	SLA                      db.FindingSLA
	Buckets                  map[string]*bucket
}

func (d *daily) get(dim dimension) *bucket {
	k := dim.key()
	if d.Buckets[k] == nil {
		d.Buckets[k] = &bucket{dimension: dim, FindingMetricBucket: db.FindingMetricBucket{DepartmentID: dim.DepartmentID, BusinessLineID: dim.LineID, OwnerID: dim.OwnerID, Severity: dim.Severity, VulnClass: dim.Class, Samples: map[string][]float64{}}, OverdueItems: []db.FindingOverdue{}}
	}
	return d.Buckets[k]
}

type state struct {
	ID                                                        int64
	Severity, Class, Status                                   string
	Discovered, Cycle, Dispatched, Fixed, Retest, ReworkStart time.Time
	FirstRetest                                               bool
	Reworks                                                   int
}

func active(s string) bool { return s == "pending" || s == "in_progress" || s == "confirmed" }
func closed(s string) bool { return s == "fixed" || s == "resolved" }
func sample(b *bucket, key string, start, end time.Time) {
	if !start.IsZero() && !end.Before(start) {
		b.Samples[key] = append(b.Samples[key], end.Sub(start).Hours())
	}
}

// Each completed step uses the ending event's immutable IDs. Legacy text fields
// in metadata never become organization IDs, even when their names match.
func replay(states map[int64]*state, e event, day *daily) error {
	var m struct {
		Severity  string    `json:"severity"`
		Class     string    `json:"vulnclass"`
		Status    string    `json:"status"`
		Verdict   string    `json:"verdict"`
		Requested time.Time `json:"requested_at"`
	}
	if err := json.Unmarshal(e.Metadata, &m); err != nil {
		return err
	}
	s := states[e.FindingID]
	if s == nil {
		s = &state{ID: e.FindingID, Status: e.OldStatus}
		states[e.FindingID] = s
	}
	if e.Kind == "discovered" {
		s.Discovered = e.At
		s.Cycle = e.At
		s.Status = e.NewStatus
		s.Severity = m.Severity
		s.Class = m.Class
	}
	if e.Kind == "classification_changed" || e.Kind == "baseline" {
		s.Severity = m.Severity
		s.Class = m.Class
	}
	dim := dimension{e.DepartmentID, e.LineID, e.OwnerID, s.Severity, s.Class}
	if e.Version == 0 || e.Quality == "unknown" {
		dim.DepartmentID = nil
		dim.LineID = nil
		dim.OwnerID = nil
	}
	b := day.get(dim)
	if e.Version == 0 || e.Quality == "unknown" {
		b.UnknownEvents++
	}
	if e.Kind == "discovered" {
		b.New++
		return nil
	}
	if s.Discovered.IsZero() {
		b.MissingDiscovery++
		day.QualityKnown = false
	}
	next := s.Status
	switch e.Kind {
	case "baseline":
		s.Status = e.NewStatus
		return nil
	case "removed":
		s.Status = "removed"
		return nil
	case "dispatched":
		sample(b, "dispatch", s.Cycle, e.At)
		s.Dispatched = e.At
	case "developer_fixed":
		sample(b, "development", s.Dispatched, e.At)
		s.Fixed = e.At
	case "retest_requested":
		sample(b, "waiting", s.Fixed, e.At)
		s.Retest = e.At
	case "retest_completed":
		if m.Status != "completed" {
			b.RetestOther++
			break
		}
		if !s.Discovered.IsZero() && !s.FirstRetest && (m.Verdict == "fixed" || m.Verdict == "reproduced") {
			s.FirstRetest = true
			b.FirstRetestTotal++
			if m.Verdict == "fixed" {
				b.FirstRetestPassed++
			}
		}
		switch m.Verdict {
		case "fixed":
			b.RetestPassed++
			if !s.Discovered.IsZero() && !closed(s.Status) {
				b.VerifiedFixes++
				b.ReworkRounds += s.Reworks
				sample(b, "rework", s.ReworkStart, e.At)
			}
			sample(b, "verification", m.Requested, e.At)
			next = "fixed"
		case "reproduced":
			b.RetestFailed++
			s.Reworks++
			if s.ReworkStart.IsZero() {
				s.ReworkStart = e.At
			}
			if closed(s.Status) {
				next = "confirmed"
			}
			s.Cycle = e.At
			s.Dispatched = time.Time{}
			s.Fixed = time.Time{}
			s.Retest = time.Time{}
		default:
			b.RetestOther++
		}
	case "status_changed":
		next = e.NewStatus
	}
	if closed(next) && !closed(s.Status) {
		b.Closed++
		sample(b, "closure", s.Discovered, e.At)
		if e.SLADays != nil && *e.SLADays > 0 && !s.Discovered.IsZero() && !e.At.Before(s.Discovered) {
			b.SLAClosed++
			if !e.At.After(s.Discovered.Add(time.Duration(*e.SLADays) * 24 * time.Hour)) {
				b.SLAMet++
			}
		} else {
			b.MissingSLAClosed++
		}
		s.Reworks = 0
		s.ReworkStart = time.Time{}
	}
	if active(next) && closed(s.Status) {
		b.Reopened++
		s.Cycle = e.At
		s.Dispatched = time.Time{}
		s.Fixed = time.Time{}
		s.Retest = time.Time{}
		if e.Kind != "retest_completed" {
			s.Reworks = 0
			s.ReworkStart = time.Time{}
		}
	}
	s.Status = next
	return nil
}
