package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/agent-ecosystem/agentminutes"
	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/spf13/cobra"
)

func newStatsCmd() *cobra.Command {
	var (
		pf               parseFlags
		output           string
		includeSubagents bool
		root             string
	)
	cmd := &cobra.Command{
		Use:   "stats <transcript>",
		Short: "Summarize a session's behavior: tool mix, bytes retrieved, timing, tokens",
		Long: `Stats parses a transcript and prints a behavioral summary as JSON:
tool selection by name and kind, call/error/orphan counts, result bytes
(with raw fetch sizes where recorded), per-tool latency, wall time, token
totals, observed models, and the final answer text.

With --include-subagents the transcript is treated as a task's parent:
the subagent transcripts the harness wrote for it are gathered through
the harness's own discovery rules (under --root, or the default root),
and the output is the task-scope summary: every transcript summarized on
its own, an aggregate in the same shape, and a per-agent split. Nothing
is counted twice: each transcript contributes its own numbers, and a
harness that records subagents inline contributes one transcript.

Pass "-" as the transcript to read from stdin (not with --include-subagents).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if includeSubagents {
				if args[0] == "-" {
					return fmt.Errorf("--include-subagents needs a transcript path, not stdin")
				}
				// Detection needs only the head; the task summary then
				// reads every transcript of the task itself.
				adapter, err := detectFile(pf.harnessID, args[0])
				if err != nil {
					return err
				}
				transforms, err := resolvePromotions(pf.promote, adapter.ID())
				if err != nil {
					return err
				}
				ts, err := agentminutes.Task(adapter.ID(), root, args[0], harness.Options{
					Permissive:         pf.permissive,
					HarnessVersionHint: pf.harnessVersion,
				}, transforms...)
				if err != nil {
					return err
				}
				return withOutput(output, cmd.OutOrStdout(), func(out io.Writer) error {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(ts)
				})
			}
			adapter, br, transforms, cleanup, err := pf.resolve(cmd, args[0])
			if err != nil {
				return err
			}
			defer cleanup()
			opts := harness.Options{
				Permissive:         pf.permissive,
				HarnessVersionHint: pf.harnessVersion,
			}
			s, err := harness.Parse(adapter, br, opts, transforms...)
			if err != nil {
				return err
			}
			return withOutput(output, cmd.OutOrStdout(), func(out io.Writer) error {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(s.Stats())
			})
		},
	}
	pf.register(cmd)
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to a file instead of stdout")
	cmd.Flags().BoolVar(&includeSubagents, "include-subagents", false, "summarize the whole task: gather the subagent transcripts and report per-transcript, aggregate, and per-agent numbers")
	cmd.Flags().StringVar(&root, "root", "", "transcript root to gather subagents under (default: the harness's default root)")
	return cmd
}

// detectFile resolves the adapter for a transcript on disk from an explicit
// --harness value or by sniffing its head, and closes the file again.
func detectFile(id, path string) (harness.Adapter, error) {
	if id != "" && id != "auto" {
		return agentminutes.AdapterFor(harness.ID(id))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only
	return resolveAdapter(id, bufio.NewReaderSize(f, harness.SniffSize))
}
