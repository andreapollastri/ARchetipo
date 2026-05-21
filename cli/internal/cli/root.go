package cli

import (
	"context"
	"io"
	"time"

	"github.com/spf13/cobra"

	// Concrete connectors register themselves via init().
	_ "github.com/techreloaded-ar/ARchetipo/cli/internal/connector/builtin"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/version"
)

// Execute runs the archetipo CLI with the given args and returns the process
// exit code. Stdin/stdout/stderr are taken as parameters so tests can drive the
// CLI without touching the real OS streams.
//
// Exit codes follow the public CLI runtime contract:
//
//	0  ok
//	1  generic error
//	2  input/validation error
//	3  connector error (auth, network, gh)
//	4  precondition missing (e.g. backlog absent)
//
// On error, the JSON envelope is written to stderr exactly once: sub-commands
// return typed errors and Execute serializes them, so handlers don't have to.
func Execute(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	notifier := version.NewNotifier(version.NotifierConfig{
		PackageName: npmPackageName,
		UpdateCmd:   "archetipo update",
		CacheTTL:    24 * time.Hour,
		HTTPTimeout: 2 * time.Second,
	}, version.Version)
	notifier.Start()
	defer notifier.Print(stderr)

	root := newRootCmd(stdin, stdout, stderr)
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		iox.WriteError(stderr, err)
		return exitCodeFor(err)
	}
	return 0
}

func newRootCmd(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "archetipo",
		Short:         "ARchetipo connector CLI",
		Long:          "Deterministic CLI implementing the ARchetipo workflow operations (file and github).",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
	}
	cmd.SetVersionTemplate(versionLine())
	cmd.SetContext(context.Background())

	s := streams{in: stdin, out: stdout, err: stderr}
	cmd.AddCommand(
		newConfigCmd(s),
		newInitProjectCmd(s),
		newUninstallCmd(s),
		newUpdateCmd(s),
		newPRDCmd(s),
		newSpecCmd(s),
		newTaskCmd(s),
		newViewCmd(s),
		newVersionCmd(s),
	)
	return cmd
}

// exitCodeFor maps a returned error to the documented exit code. Specific
// error types defined in internal/iox carry their own code; everything else is
// a generic error (1).
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	if coded, ok := err.(interface{ ExitCode() int }); ok {
		return coded.ExitCode()
	}
	return 1
}
