package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/version"
)

const npmPackageName = "@techreloaded/archetipo"

func newUpdateCmd(s streams) *cobra.Command {
	var check bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the archetipo CLI to the latest version via npm",
		Long: "Runs `npm i -g " + npmPackageName + "@latest` to update the global installation.\n" +
			"Use --check to only compare versions, or --dry-run to see the command without running it.\n" +
			"This only updates the CLI: skills already copied into projects or ~/.{tool}/skills/ stay at their current version. Re-run `archetipo init` to refresh them.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runUpdate(s, check, dryRun)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Compare installed version against the npm registry latest and exit")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the npm command that would run without executing it")
	return cmd
}

func runUpdate(s streams, check, dryRun bool) error {
	if check {
		latest, err := fetchLatestVersion(4 * time.Second)
		if err != nil {
			return iox.NewConnector(iox.CodeConnectorNetwork, "cannot reach npm registry", "check internet connection or use --dry-run", err)
		}
		if latest == version.Version {
			fmt.Fprintf(s.out, "archetipo %s is up to date.\n", version.Version)
			return nil
		}
		fmt.Fprintf(s.out, "Update available: %s → %s\nRun: archetipo update\n", version.Version, latest)
		return nil
	}

	cmdLine := []string{"npm", "i", "-g", npmPackageName + "@latest"}
	if dryRun {
		fmt.Fprintln(s.out, joinArgs(cmdLine))
		return nil
	}

	if _, err := exec.LookPath("npm"); err != nil {
		return iox.NewPrecondition(
			"npm not found in PATH",
			"install Node.js or update manually with `npm i -g "+npmPackageName+"@latest`",
			err,
		)
	}

	c := exec.Command(cmdLine[0], cmdLine[1:]...)
	c.Stdin = s.in
	c.Stdout = s.out
	c.Stderr = s.err
	c.Env = os.Environ()
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return iox.NewInternal("npm invocation failed", err)
	}
	return nil
}

func joinArgs(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// fetchLatestVersion queries the npm registry for the `latest` dist-tag of the
// archetipo package and returns the resolved version string.
func fetchLatestVersion(timeout time.Duration) (string, error) {
	client := &http.Client{Timeout: timeout}
	url := "https://registry.npmjs.org/" + npmPackageName + "/latest"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "archetipo/"+version.Version)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Version == "" {
		return "", fmt.Errorf("registry response missing version field")
	}
	return body.Version, nil
}
