package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/version"
)

// doctorCheck is one diagnostic line. Skipped checks count as neither pass nor
// failure (e.g. gh when the connector is file).
type doctorCheck struct {
	name    string
	ok      bool
	skipped bool
	detail  string
	hint    string
}

// newDoctorCmd diagnoses the local installation: data directory, skills,
// runtime assets, project config, installed skills per tool, git and gh.
// Human-readable output (like `version`), exit code 4 when any check fails.
func newDoctorCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the ARchetipo installation and project setup",
		Long: "Checks the CLI installation (data directory, packaged skills, runtime assets), " +
			"the project setup (.archetipo/config.yaml, skills installed in tool directories) " +
			"and external dependencies (git, gh when the github connector is configured). " +
			"Exits non-zero when a check fails.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := runDoctorChecks(cmd.Context())
			failed := 0
			fmt.Fprintf(s.out, "archetipo %s\n\n", version.Version)
			for _, c := range checks {
				switch {
				case c.skipped:
					fmt.Fprintf(s.out, "- %s: %s\n", c.name, c.detail)
				case c.ok:
					fmt.Fprintf(s.out, "✓ %s: %s\n", c.name, c.detail)
				default:
					failed++
					fmt.Fprintf(s.out, "✗ %s: %s\n", c.name, c.detail)
					if c.hint != "" {
						fmt.Fprintf(s.out, "  → %s\n", c.hint)
					}
				}
			}
			fmt.Fprintln(s.out)
			if failed > 0 {
				return iox.NewPrecondition(
					fmt.Sprintf("%d doctor check(s) failed", failed),
					"see the report above for per-check fixes", nil)
			}
			fmt.Fprintln(s.out, "All checks passed.")
			return nil
		},
	}
}

func runDoctorChecks(ctx context.Context) []doctorCheck {
	var checks []doctorCheck

	// 1. Data directory (skills + runtime shipped with the CLI).
	dataDir, err := discoverDataDir()
	if err != nil {
		checks = append(checks, doctorCheck{
			name:   "data directory",
			detail: "not found",
			hint:   "set ARCHETIPO_DATA_DIR or reinstall via `npm i -g @techreloaded/archetipo`",
		})
	} else {
		checks = append(checks, doctorCheck{name: "data directory", ok: true, detail: dataDir})
		checks = append(checks, checkPackagedSkills(dataDir))
		checks = append(checks, checkRuntimeAssets(dataDir))
	}

	// 2. Project config.
	cfg, cfgErr := config.Load(".")
	if cfgErr != nil {
		checks = append(checks, doctorCheck{
			name:   "project config",
			detail: cfgErr.Error(),
			hint:   "fix .archetipo/config.yaml or delete it to fall back to defaults",
		})
	} else {
		detail := fmt.Sprintf("connector %q, project root %s", cfg.Connector, cfg.ProjectRoot)
		checks = append(checks, doctorCheck{name: "project config", ok: true, detail: detail})
	}

	// 3. Skills installed in the project's tool directories.
	checks = append(checks, checkInstalledSkills())

	// 4. git availability.
	if gitPath, err := exec.LookPath("git"); err != nil {
		checks = append(checks, doctorCheck{
			name:   "git",
			detail: "not found in PATH",
			hint:   "install git; the worktree workflow and the github connector require it",
		})
	} else {
		checks = append(checks, doctorCheck{name: "git", ok: true, detail: gitPath})
	}

	// 5. gh availability + auth, only relevant for the github connector.
	if cfgErr == nil && cfg.Connector == config.ConnectorGitHub {
		checks = append(checks, checkGH(ctx))
	} else {
		checks = append(checks, doctorCheck{name: "gh", skipped: true, detail: "skipped (connector is not github)"})
	}

	return checks
}

func checkPackagedSkills(dataDir string) doctorCheck {
	skillsDir := filepath.Join(dataDir, "skills")
	var missing []string
	for _, sk := range allSkills {
		if _, err := os.Stat(filepath.Join(skillsDir, sk)); err != nil {
			missing = append(missing, sk)
		}
	}
	if len(missing) > 0 {
		return doctorCheck{
			name:   "packaged skills",
			detail: fmt.Sprintf("%d/%d present, missing: %s", len(allSkills)-len(missing), len(allSkills), strings.Join(missing, ", ")),
			hint:   "reinstall the CLI via `npm i -g @techreloaded/archetipo`",
		}
	}
	return doctorCheck{name: "packaged skills", ok: true, detail: fmt.Sprintf("%d/%d present", len(allSkills), len(allSkills))}
}

func checkRuntimeAssets(dataDir string) doctorCheck {
	// Mirror installRuntimeAssets: runtime/ (npm layout) or .archetipo/ (repo layout).
	for _, dir := range []string{filepath.Join(dataDir, "runtime"), filepath.Join(dataDir, ".archetipo")} {
		if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err == nil {
			return doctorCheck{name: "runtime assets", ok: true, detail: dir}
		}
	}
	return doctorCheck{
		name:   "runtime assets",
		detail: "config.yaml template not found in the package",
		hint:   "reinstall the CLI via `npm i -g @techreloaded/archetipo`",
	}
}

// checkInstalledSkills reports, for each tool directory present in the
// project, how many ARchetipo skills are installed. No tool directory at all
// is a failure: `archetipo init` has not been run here.
func checkInstalledSkills() doctorCheck {
	var found []string
	var stale []string
	for _, t := range allTools {
		if _, err := os.Stat(t.ProjectPath); err != nil {
			continue
		}
		installed := 0
		for _, sk := range allSkills {
			if _, err := os.Stat(filepath.Join(t.ProjectPath, sk)); err == nil {
				installed++
			}
		}
		if installed == 0 {
			continue
		}
		found = append(found, fmt.Sprintf("%s %d/%d", t.Key, installed, len(allSkills)))
		if installed < len(allSkills) {
			stale = append(stale, t.Key)
		}
	}
	if len(found) == 0 {
		return doctorCheck{
			name:   "installed skills",
			detail: "no ARchetipo skills found in any tool directory",
			hint:   "run `archetipo init` in this project",
		}
	}
	if len(stale) > 0 {
		return doctorCheck{
			name:   "installed skills",
			detail: strings.Join(found, ", "),
			hint:   "some skills are missing for " + strings.Join(stale, ", ") + "; re-run `archetipo init` to refresh them",
		}
	}
	return doctorCheck{name: "installed skills", ok: true, detail: strings.Join(found, ", ")}
}

func checkGH(ctx context.Context) doctorCheck {
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return doctorCheck{
			name:   "gh",
			detail: "not found in PATH",
			hint:   "install the GitHub CLI: https://cli.github.com",
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "gh", "auth", "status").CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return doctorCheck{
			name:   "gh",
			detail: "not authenticated: " + firstLine(detail),
			hint:   "run `gh auth login`",
		}
	}
	return doctorCheck{name: "gh", ok: true, detail: ghPath + " (authenticated)"}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
