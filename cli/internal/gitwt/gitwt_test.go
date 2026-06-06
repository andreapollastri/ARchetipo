package gitwt

import (
	"context"
	"testing"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

// These tests run against a non-git temp dir: git ancestry probes return false,
// so any blocker that has a recorded branch is treated as unintegrated. That is
// enough to exercise the 0 / 1 / >=2 branch-selection logic deterministically
// without a git fixture.

func cfg() domain.WorktreeConfig {
	return domain.WorktreeConfig{Enabled: true, Base: "main", Dir: ".wt", BranchPrefix: "archetipo/"}
}

func TestForkRef_NoBlockers_ForksFromBase(t *testing.T) {
	spec := domain.Spec{Code: "US-001"}
	ref, err := ForkRef(context.Background(), t.TempDir(), cfg(), spec, []domain.Spec{spec})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref != "main" {
		t.Fatalf("want base 'main', got %q", ref)
	}
}

func TestForkRef_BlockerWithoutBranch_IsIntegrated(t *testing.T) {
	all := []domain.Spec{
		{Code: "US-001"}, // no branch -> considered integrated
		{Code: "US-002", BlockedBy: []string{"US-001"}},
	}
	ref, err := ForkRef(context.Background(), t.TempDir(), cfg(), all[1], all)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref != "main" {
		t.Fatalf("want base 'main', got %q", ref)
	}
}

func TestForkRef_SingleUnmergedBlocker_StacksOnBranch(t *testing.T) {
	all := []domain.Spec{
		{Code: "US-001", Branch: "archetipo/US-001"},
		{Code: "US-002", BlockedBy: []string{"US-001"}},
	}
	ref, err := ForkRef(context.Background(), t.TempDir(), cfg(), all[1], all)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref != "archetipo/US-001" {
		t.Fatalf("want stack on blocker branch, got %q", ref)
	}
}

func TestForkRef_MultipleUnmergedBlockers_Conflict(t *testing.T) {
	all := []domain.Spec{
		{Code: "US-001", Branch: "archetipo/US-001"},
		{Code: "US-002", Branch: "archetipo/US-002"},
		{Code: "US-003", BlockedBy: []string{"US-001", "US-002"}},
	}
	_, err := ForkRef(context.Background(), t.TempDir(), cfg(), all[2], all)
	if err == nil {
		t.Fatal("expected a conflict error for multiple unmerged blockers")
	}
}
