package metrics

import (
	"testing"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

func TestFlowTimes(t *testing.T) {
	h := func(status domain.Status, at string) domain.StatusChange {
		return domain.StatusChange{Status: status, At: at}
	}
	tests := []struct {
		name      string
		history   []domain.StatusChange
		wantCycle int64
		wantLead  int64
		wantOK    bool
	}{
		{
			name: "straight flow",
			history: []domain.StatusChange{
				h(domain.StatusTodo, "2026-06-01T00:00:00Z"),
				h(domain.StatusInProgress, "2026-06-02T00:00:00Z"),
				h(domain.StatusDone, "2026-06-03T00:00:00Z"),
			},
			wantCycle: 86400, wantLead: 172800, wantOK: true,
		},
		{
			name: "rework loop measures from first start to last done",
			history: []domain.StatusChange{
				h(domain.StatusTodo, "2026-06-01T00:00:00Z"),
				h(domain.StatusInProgress, "2026-06-01T12:00:00Z"),
				h(domain.StatusReview, "2026-06-02T00:00:00Z"),
				h(domain.StatusTodo, "2026-06-02T06:00:00Z"),
				h(domain.StatusInProgress, "2026-06-02T12:00:00Z"),
				h(domain.StatusDone, "2026-06-03T12:00:00Z"),
			},
			wantCycle: 172800, wantLead: 216000, wantOK: true,
		},
		{
			name: "moved straight to done degenerates cycle to lead",
			history: []domain.StatusChange{
				h(domain.StatusTodo, "2026-06-01T00:00:00Z"),
				h(domain.StatusDone, "2026-06-02T00:00:00Z"),
			},
			wantCycle: 86400, wantLead: 86400, wantOK: true,
		},
		{
			name:    "no done entry",
			history: []domain.StatusChange{h(domain.StatusTodo, "2026-06-01T00:00:00Z")},
			wantOK:  false,
		},
		{
			name:   "empty history",
			wantOK: false,
		},
		{
			name: "unparsable timestamp",
			history: []domain.StatusChange{
				h(domain.StatusTodo, "yesterday"),
				h(domain.StatusDone, "2026-06-02T00:00:00Z"),
			},
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cycle, lead, ok := flowTimes(tc.history)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if cycle != tc.wantCycle || lead != tc.wantLead {
				t.Fatalf("flowTimes() = (%d, %d), want (%d, %d)", cycle, lead, tc.wantCycle, tc.wantLead)
			}
		})
	}
}

func TestCompute_EmptyBacklog(t *testing.T) {
	data := Compute(nil)
	if data.Totals.Specs != 0 || data.Totals.CompletionPct != 0 {
		t.Fatalf("unexpected totals for empty backlog: %+v", data.Totals)
	}
	if len(data.ByStatus) != 5 {
		t.Fatalf("expected 5 status buckets, got %d", len(data.ByStatus))
	}
	if data.Flow != nil {
		t.Fatalf("expected no flow metrics, got %+v", data.Flow)
	}
}

func TestCompute_FallsBackToSpecCountWithoutPoints(t *testing.T) {
	specs := []domain.Spec{
		{Code: "US-001", Status: domain.StatusDone},
		{Code: "US-002", Status: domain.StatusTodo},
	}
	data := Compute(specs)
	if data.Totals.CompletionPct != 50 {
		t.Fatalf("expected completion 50%% by spec count, got %v", data.Totals.CompletionPct)
	}
}
