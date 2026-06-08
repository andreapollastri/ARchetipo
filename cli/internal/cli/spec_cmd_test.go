package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

// TestResolveWorkdir pins the resolution precedence: the real filesystem state
// wins over the persisted spec.Worktree field, so a dropped link never hides an
// existing worktree (the bug this resolution exists to prevent).
func TestResolveWorkdir(t *testing.T) {
	root := t.TempDir()
	wtCfg := domain.WorktreeConfig{Enabled: true, Base: "main", Dir: ".archetipo/worktrees", BranchPrefix: "archetipo/"}
	cfg := config.Config{ProjectRoot: root, Worktree: wtCfg}

	// Create the conventional worktree directory for US-001 on disk.
	wtAbs := filepath.Join(root, ".archetipo", "worktrees", "US-001")
	if err := os.MkdirAll(wtAbs, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	tests := []struct {
		name string
		cfg  config.Config
		spec domain.Spec
		want string
	}{
		{
			name: "worktree on disk wins even when spec field is empty",
			cfg:  cfg,
			spec: domain.Spec{Code: "US-001"}, // Worktree field intentionally empty
			want: wtAbs,
		},
		{
			name: "no worktree on disk falls back to project root",
			cfg:  cfg,
			spec: domain.Spec{Code: "US-999"},
			want: root,
		},
		{
			name: "workflow disabled honors persisted field",
			cfg:  config.Config{ProjectRoot: root, Worktree: domain.WorktreeConfig{Enabled: false}},
			spec: domain.Spec{Code: "US-002", Worktree: "some/where"},
			want: filepath.Join(root, "some/where"),
		},
		{
			name: "workflow disabled and no field falls back to project root",
			cfg:  config.Config{ProjectRoot: root, Worktree: domain.WorktreeConfig{Enabled: false}},
			spec: domain.Spec{Code: "US-003"},
			want: root,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveWorkdir(tc.cfg, tc.spec); got != tc.want {
				t.Errorf("resolveWorkdir = %q, want %q", got, tc.want)
			}
		})
	}
}
