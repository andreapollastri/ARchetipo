// Package metrics aggregates a backlog snapshot into delivery metrics. It is
// shared by `archetipo metrics` (CLI envelope) and the web viewer
// (`GET /api/metrics`): both expose the same Data shape.
package metrics

import (
	"math"
	"time"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

// Data is the metrics payload: a snapshot of backlog progress plus flow times
// derived from the specs' status history.
type Data struct {
	Totals   Totals         `json:"totals"`
	ByStatus []StatusBucket `json:"by_status"`
	ByEpic   []EpicBucket   `json:"by_epic,omitempty"`
	Rework   []string       `json:"rework,omitempty"`
	Blocked  []BlockedSpec  `json:"blocked,omitempty"`
	Flow     *Flow          `json:"flow,omitempty"`
}

type Totals struct {
	Specs         int     `json:"specs"`
	Points        int     `json:"points"`
	DoneSpecs     int     `json:"done_specs"`
	DonePoints    int     `json:"done_points"`
	CompletionPct float64 `json:"completion_pct"`
	WIPSpecs      int     `json:"wip_specs"`
}

type StatusBucket struct {
	Status domain.Status `json:"status"`
	Specs  int           `json:"specs"`
	Points int           `json:"points"`
}

type EpicBucket struct {
	Code          string  `json:"code"`
	Title         string  `json:"title,omitempty"`
	Specs         int     `json:"specs"`
	Points        int     `json:"points"`
	DoneSpecs     int     `json:"done_specs"`
	DonePoints    int     `json:"done_points"`
	CompletionPct float64 `json:"completion_pct"`
}

type BlockedSpec struct {
	Code      string   `json:"code"`
	BlockedBy []string `json:"blocked_by"`
}

// Flow aggregates cycle time (first IN PROGRESS → last DONE) and lead time
// (creation → last DONE) over the DONE specs whose history carries the needed
// entries. Specs without history (e.g. github connector, or created before
// history recording) are simply not measured.
type Flow struct {
	MeasuredSpecs   int   `json:"measured_specs"`
	AvgCycleSeconds int64 `json:"avg_cycle_seconds"`
	AvgLeadSeconds  int64 `json:"avg_lead_seconds"`
}

// Compute aggregates the backlog snapshot into metrics. Specs order is
// preserved in the per-epic breakdown (first-seen order).
func Compute(specs []domain.Spec) Data {
	data := Data{}
	statusOrder := []domain.Status{
		domain.StatusTodo, domain.StatusPlanned, domain.StatusInProgress,
		domain.StatusReview, domain.StatusDone,
	}
	byStatus := map[domain.Status]*StatusBucket{}
	for _, st := range statusOrder {
		byStatus[st] = &StatusBucket{Status: st}
	}
	statusOf := make(map[string]domain.Status, len(specs))
	for _, sp := range specs {
		statusOf[sp.Code] = sp.Status
	}

	epicIndex := map[string]int{}
	var flowCycleSum, flowLeadSum int64
	flowMeasured := 0

	for _, sp := range specs {
		data.Totals.Specs++
		data.Totals.Points += sp.Points
		if b, ok := byStatus[sp.Status]; ok {
			b.Specs++
			b.Points += sp.Points
		}
		switch sp.Status {
		case domain.StatusDone:
			data.Totals.DoneSpecs++
			data.Totals.DonePoints += sp.Points
		case domain.StatusInProgress, domain.StatusReview:
			data.Totals.WIPSpecs++
		}

		if sp.Epic.Code != "" {
			idx, ok := epicIndex[sp.Epic.Code]
			if !ok {
				idx = len(data.ByEpic)
				epicIndex[sp.Epic.Code] = idx
				data.ByEpic = append(data.ByEpic, EpicBucket{Code: sp.Epic.Code, Title: sp.Epic.Title})
			}
			e := &data.ByEpic[idx]
			e.Specs++
			e.Points += sp.Points
			if sp.Status == domain.StatusDone {
				e.DoneSpecs++
				e.DonePoints += sp.Points
			}
		}

		if sp.Rework {
			data.Rework = append(data.Rework, sp.Code)
		}
		if sp.Status != domain.StatusDone {
			var unmet []string
			for _, dep := range sp.BlockedBy {
				if statusOf[dep] != domain.StatusDone {
					unmet = append(unmet, dep)
				}
			}
			if len(unmet) > 0 {
				data.Blocked = append(data.Blocked, BlockedSpec{Code: sp.Code, BlockedBy: unmet})
			}
		}

		if sp.Status == domain.StatusDone {
			if cycle, lead, ok := flowTimes(sp.History); ok {
				flowMeasured++
				flowCycleSum += cycle
				flowLeadSum += lead
			}
		}
	}

	data.Totals.CompletionPct = completionPct(data.Totals.DonePoints, data.Totals.Points, data.Totals.DoneSpecs, data.Totals.Specs)
	for i := range data.ByEpic {
		e := &data.ByEpic[i]
		e.CompletionPct = completionPct(e.DonePoints, e.Points, e.DoneSpecs, e.Specs)
	}
	for _, st := range statusOrder {
		data.ByStatus = append(data.ByStatus, *byStatus[st])
	}
	if flowMeasured > 0 {
		data.Flow = &Flow{
			MeasuredSpecs:   flowMeasured,
			AvgCycleSeconds: flowCycleSum / int64(flowMeasured),
			AvgLeadSeconds:  flowLeadSum / int64(flowMeasured),
		}
	}
	return data
}

// completionPct prefers points (effort-weighted) and falls back to spec count
// when the backlog carries no points at all.
func completionPct(donePoints, totalPoints, doneSpecs, totalSpecs int) float64 {
	done, total := donePoints, totalPoints
	if total == 0 {
		done, total = doneSpecs, totalSpecs
	}
	if total == 0 {
		return 0
	}
	return math.Round(float64(done)/float64(total)*1000) / 10
}

// flowTimes derives cycle time (first IN PROGRESS → last DONE) and lead time
// (first entry → last DONE) from a spec's status history. Returns ok=false
// when the history lacks the needed entries or carries unparsable timestamps.
func flowTimes(history []domain.StatusChange) (cycleSeconds, leadSeconds int64, ok bool) {
	var created, started, done time.Time
	for _, h := range history {
		at, err := time.Parse(time.RFC3339, h.At)
		if err != nil {
			return 0, 0, false
		}
		if created.IsZero() {
			created = at
		}
		if h.Status == domain.StatusInProgress && started.IsZero() {
			started = at
		}
		if h.Status == domain.StatusDone {
			done = at
		}
	}
	if created.IsZero() || done.IsZero() || done.Before(created) {
		return 0, 0, false
	}
	if started.IsZero() || done.Before(started) {
		// Never started explicitly (e.g. moved straight to done): cycle
		// degenerates to lead.
		started = created
	}
	return int64(done.Sub(started).Seconds()), int64(done.Sub(created).Seconds()), true
}
