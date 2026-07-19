package main

import (
	"encoding/json"
	"io"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/spf13/cobra"
)

func newStatsCmd() *cobra.Command {
	var (
		pf     parseFlags
		output string
	)
	cmd := &cobra.Command{
		Use:   "stats <transcript>",
		Short: "Summarize a session's behavior: tool mix, bytes retrieved, timing, tokens",
		Long: `Stats parses a transcript and prints a behavioral summary as JSON:
tool selection by name and kind, call/error/orphan counts, result bytes
(with raw fetch sizes where recorded), per-tool latency, wall time, token
totals, observed models, and the final answer text.

Pass "-" as the transcript to read from stdin.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
	return cmd
}
