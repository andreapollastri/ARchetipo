package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

type toolDef struct {
	Key         string
	Name        string
	ProjectPath string // relative to cwd, e.g. ".claude/skills"
}

var allTools = []toolDef{
	{Key: "claude", Name: "Claude Code", ProjectPath: ".claude/skills"},
	{Key: "codex", Name: "Codex", ProjectPath: ".agents/skills"},
	{Key: "gemini", Name: "Gemini CLI", ProjectPath: ".gemini/skills"},
	{Key: "opencode", Name: "OpenCode", ProjectPath: ".opencode/skills"},
	{Key: "copilot", Name: "GitHub Copilot", ProjectPath: ".github/skills"},
	{Key: "pi", Name: "Pi", ProjectPath: ".pi/skills"},
}

var allSkills = []string{
	"archetipo-autopilot",
	"archetipo-design",
	"archetipo-implement",
	"archetipo-inception",
	"archetipo-plan",
	"archetipo-spec",
}

func newInitProjectCmd(s streams) *cobra.Command {
	var toolFlags []string
	var connectorFlag string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Install ARchetipo skills into the project",
		Long: "Copies ARchetipo skills into the selected tool directories. " +
			"Also creates .archetipo/config.yaml and .archetipo/shared-runtime.md in the current directory.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runInitProject(s, toolFlags, connectorFlag)
		},
	}
	cmd.Flags().StringSliceVar(&toolFlags, "tool", nil, "Tool key(s) to install for: claude, codex, gemini, opencode, copilot, pi. Repeat or comma-separate.")
	cmd.Flags().StringVar(&connectorFlag, "connector", "", "Connector for .archetipo/config.yaml: file|github")
	return cmd
}

func runInitProject(s streams, toolFlags []string, connectorFlag string) error {
	dataDir, err := discoverDataDir()
	if err != nil {
		return err
	}
	skillsDir := filepath.Join(dataDir, "skills")
	runtimeDir := filepath.Join(dataDir, "runtime")

	if _, statErr := os.Stat(skillsDir); statErr != nil {
		return iox.NewPrecondition(
			"skills directory not found",
			"set ARCHETIPO_DATA_DIR to the package root, or reinstall the CLI via `npm i -g @techreloaded/archetipo`",
			statErr,
		)
	}

	tools, err := resolveToolFlags(toolFlags)
	if err != nil {
		return err
	}
	if len(tools) == 0 {
		tools, err = pickToolsInteractive(s)
		if err != nil {
			return err
		}
	}
	if len(tools) == 0 {
		fmt.Fprintln(s.out, "No tools selected.")
		return nil
	}

	var conn string
	if connectorFlag != "" {
		if connectorFlag != "file" && connectorFlag != "github" {
			return iox.NewInvalidInput("--connector must be 'file' or 'github'", "", nil)
		}
		conn = connectorFlag
	} else {
		conn, err = pickConnectorInteractive(s)
		if err != nil {
			return err
		}
	}

	fmt.Fprintln(s.out, "Installing...")
	for _, t := range tools {
		target := t.ProjectPath
		if err := os.MkdirAll(target, 0o755); err != nil {
			return iox.NewInternal("cannot create "+target, err)
		}
		for _, sk := range allSkills {
			src := filepath.Join(skillsDir, sk)
			dst := filepath.Join(target, sk)
			if _, err := os.Stat(src); err != nil {
				return iox.NewPrecondition("skill missing in package: "+sk, "reinstall the CLI", err)
			}
			if err := os.RemoveAll(dst); err != nil {
				return iox.NewInternal("cannot clean "+dst, err)
			}
			if err := copyTree(src, dst); err != nil {
				return iox.NewInternal("copy "+sk, err)
			}
		}
		fmt.Fprintf(s.out, "  ✓ %s → %s\n", t.Name, target)
	}

	if err := installRuntimeAssets(s, runtimeDir, conn); err != nil {
		return err
	}

	fmt.Fprintln(s.out, "Done.")
	return nil
}

// discoverDataDir returns the directory containing skills/ and runtime/.
// Resolution order:
//  1. ARCHETIPO_DATA_DIR env var (set by the npm shim)
//  2. directory of the running binary, looking for skills/ alongside or in parent
//  3. the repo layout when running from source (skills/ + .archetipo/ at repo root)
func discoverDataDir() (string, error) {
	if env := strings.TrimSpace(os.Getenv("ARCHETIPO_DATA_DIR")); env != "" {
		if _, err := os.Stat(filepath.Join(env, "skills")); err == nil {
			return env, nil
		}
	}
	exe, err := os.Executable()
	if err == nil {
		resolved, _ := filepath.EvalSymlinks(exe)
		if resolved != "" {
			exe = resolved
		}
		for _, base := range []string{filepath.Dir(exe), filepath.Dir(filepath.Dir(exe))} {
			if _, err := os.Stat(filepath.Join(base, "skills")); err == nil {
				return base, nil
			}
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if _, statErr := os.Stat(filepath.Join(cwd, "skills")); statErr == nil {
			return repoFallbackDataDir(cwd), nil
		}
	}
	return "", iox.NewPrecondition(
		"could not locate ARchetipo data directory",
		"set ARCHETIPO_DATA_DIR or reinstall via `npm i -g @techreloaded/archetipo`",
		nil,
	)
}

// repoFallbackDataDir maps the repo layout (skills/ + .archetipo/) onto the
// expected runtime layout by treating .archetipo/ as runtime/.
func repoFallbackDataDir(repoRoot string) string {
	// In-place: the init code uses dataDir/skills and dataDir/runtime.
	// The repo has skills/ and .archetipo/. We materialize a virtual mapping
	// by symlinking is overkill — instead the caller reads runtime via
	// runtimeRootFor(dataDir) which checks both layouts. We return the repo
	// root and let installRuntimeAssets look in either runtime/ or .archetipo/.
	return repoRoot
}

// runtimeRootFor returns the directory holding config.yaml + shared-runtime.md,
// preferring runtime/ (npm layout) and falling back to .archetipo/ (repo layout).
func runtimeRootFor(dataDir string) string {
	candidate := filepath.Join(dataDir, "runtime")
	if _, err := os.Stat(filepath.Join(candidate, "config.yaml")); err == nil {
		return candidate
	}
	return filepath.Join(dataDir, ".archetipo")
}

func resolveToolFlags(flags []string) ([]toolDef, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	keysByKey := map[string]toolDef{}
	for _, t := range allTools {
		keysByKey[t.Key] = t
	}
	seen := map[string]struct{}{}
	out := []toolDef{}
	for _, raw := range flags {
		key := strings.ToLower(strings.TrimSpace(raw))
		t, ok := keysByKey[key]
		if !ok {
			return nil, iox.NewInvalidInput("unknown tool: "+raw, "valid: claude, codex, gemini, opencode, copilot, pi", nil)
		}
		if _, dup := seen[t.Key]; dup {
			continue
		}
		seen[t.Key] = struct{}{}
		out = append(out, t)
	}
	return out, nil
}

func pickToolsInteractive(s streams) ([]toolDef, error) {
	options := allTools
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, "Select tools to install for:")
	fmt.Fprintln(s.out)
	for i, t := range options {
		fmt.Fprintf(s.out, "  %d) %s\n", i+1, t.Name)
	}
	fmt.Fprintln(s.out)
	fmt.Fprint(s.out, "Numbers separated by spaces, or 'all' (Enter to cancel): ")

	line, err := readLine(s.in)
	if err != nil {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}
	if strings.EqualFold(line, "all") {
		return options, nil
	}
	seen := map[string]struct{}{}
	out := []toolDef{}
	for _, tok := range strings.Fields(line) {
		idx, err := strconv.Atoi(tok)
		if err != nil || idx < 1 || idx > len(options) {
			return nil, iox.NewInvalidInput("invalid selection: "+tok, "use numbers from the list or 'all'", nil)
		}
		t := options[idx-1]
		if _, dup := seen[t.Key]; dup {
			continue
		}
		seen[t.Key] = struct{}{}
		out = append(out, t)
	}
	return out, nil
}

func pickConnectorInteractive(s streams) (string, error) {
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, "Select connector:")
	fmt.Fprintln(s.out, "  1) file    — backlog and planning as local Markdown files")
	fmt.Fprintln(s.out, "  2) github  — GitHub Projects v2 (requires gh CLI)")
	fmt.Fprintln(s.out)
	fmt.Fprint(s.out, "Choice [1]: ")
	line, err := readLine(s.in)
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	switch line {
	case "", "1", "file":
		return "file", nil
	case "2", "github":
		return "github", nil
	}
	return "", iox.NewInvalidInput("invalid connector choice: "+line, "enter 1 or 2", nil)
}

func installRuntimeAssets(s streams, runtimeDir, connector string) error {
	root := runtimeDir
	if _, err := os.Stat(filepath.Join(root, "config.yaml")); err != nil {
		// dataDir/runtime missing -> try repo .archetipo/
		alt := filepath.Join(filepath.Dir(runtimeDir), ".archetipo")
		if _, err := os.Stat(filepath.Join(alt, "config.yaml")); err == nil {
			root = alt
		} else {
			return iox.NewPrecondition("runtime assets not found", "package may be incomplete; reinstall the CLI", err)
		}
	}

	if err := os.MkdirAll(".archetipo", 0o755); err != nil {
		return iox.NewInternal("cannot create .archetipo/", err)
	}

	configPath := ".archetipo/config.yaml"
	if _, err := os.Stat(configPath); err == nil {
		fmt.Fprintf(s.out, "\n  ! .archetipo/config.yaml already exists. Overwrite? [s/N] ")
		line, err := readLine(s.in)
		if err != nil {
			return err
		}
		ans := strings.ToLower(strings.TrimSpace(line))
		if ans != "s" && ans != "y" {
			fmt.Fprintln(s.out, "  config left unchanged")
		} else {
			if err := writeConfig(filepath.Join(root, "config.yaml"), configPath, connector); err != nil {
				return err
			}
			fmt.Fprintf(s.out, "  ✓ .archetipo/config.yaml (connector: %s)\n", connector)
		}
	} else {
		if err := writeConfig(filepath.Join(root, "config.yaml"), configPath, connector); err != nil {
			return err
		}
		fmt.Fprintf(s.out, "  ✓ .archetipo/config.yaml (connector: %s)\n", connector)
	}

	sharedSrc := filepath.Join(root, "shared-runtime.md")
	sharedDst := ".archetipo/shared-runtime.md"
	if _, err := os.Stat(sharedSrc); err == nil {
		if err := copyFile(sharedSrc, sharedDst); err != nil {
			return iox.NewInternal("copy shared-runtime.md", err)
		}
		fmt.Fprintln(s.out, "  ✓ .archetipo/shared-runtime.md")
	}
	return nil
}

func writeConfig(src, dst, connector string) error {
	body, err := os.ReadFile(src)
	if err != nil {
		return iox.NewInternal("read config template", err)
	}
	out := setConnectorField(string(body), connector)
	if err := os.WriteFile(dst, []byte(out), 0o644); err != nil {
		return iox.NewInternal("write "+dst, err)
	}
	return nil
}

// setConnectorField rewrites the top-level `connector:` line of the YAML
// template. It only touches lines that look like `connector:` at column 0 to
// avoid clobbering nested keys.
func setConnectorField(body, connector string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trim := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trim, "connector:") {
			lines[i] = "connector: " + connector
			return strings.Join(lines, "\n")
		}
	}
	// no existing field -> prepend
	return "connector: " + connector + "\n" + body
}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst)
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func readLine(r io.Reader) (string, error) {
	if r == nil {
		return "", errors.New("no input stream")
	}
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}
