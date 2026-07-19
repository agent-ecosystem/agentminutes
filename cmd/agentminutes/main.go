// Command agentminutes parses native agent harness session transcripts
// into a unified, comparable event schema.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/agent-ecosystem/agentminutes"
	"github.com/spf13/cobra"
)

// version is stamped by goreleaser on release builds (default ldflags,
// -X main.version). Must stay a package-level var named "version" for
// that stamping to land.
var version = "dev"

// cliVersion returns the stamped release version, falling back to the
// module version Go embeds under `go install` (where no ldflags run).
func cliVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		os.Exit(1)
	}
}

// exitError carries a specific process exit code through cobra to main.
type exitError struct{ code int }

func (e *exitError) Error() string {
	return fmt.Sprintf("exit code %d", e.code)
}

// silentExit returns the exit-code sentinel with cobra's own error printing
// silenced: the command already reported everything it had to say.
func silentExit(cmd *cobra.Command, code int) error {
	cmd.SilenceErrors = true
	return &exitError{code: code}
}

// harnessList renders the registered harness IDs for flag help, quoted, in
// registry (alphabetical) order, so help text cannot drift from the
// registry.
func harnessList() string {
	adapters := agentminutes.Adapters()
	ids := make([]string, len(adapters))
	for i, a := range adapters {
		ids[i] = `"` + string(a.ID()) + `"`
	}
	return strings.Join(ids, ", ")
}

// withOutput runs fn against stdout, or against a freshly created path when
// one is given, folding the file close error into fn's.
func withOutput(path string, stdout io.Writer, fn func(io.Writer) error) error {
	if path == "" {
		return fn(stdout)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = fn(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "agentminutes",
		Short: "Parse agent session transcripts into a unified event schema",
		Long: `agentminutes parses native agent harness session logs (Antigravity
CLI, Claude Code, Codex CLI) into a unified event schema, so tool calls,
results, and messages are comparable across harnesses.`,
		Version:      cliVersion(),
		SilenceUsage: true,
	}
	root.AddCommand(newConvertCmd(), newDetectCmd(), newDriftCmd(), newSessionsCmd(), newStatsCmd())
	return root
}

// openInput opens a transcript argument, with "-" meaning stdin.
func openInput(cmd *cobra.Command, path string) (io.Reader, func(), error) {
	if path == "-" {
		return cmd.InOrStdin(), func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}
