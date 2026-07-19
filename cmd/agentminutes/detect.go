package main

import (
	"fmt"
	"io"

	"github.com/agent-ecosystem/agentminutes"
	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/spf13/cobra"
)

func newDetectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect <transcript>",
		Short: "Identify which harness wrote a transcript",
		Long: `Detect inspects a transcript's opening bytes and reports the harness
that wrote it, with the match confidence (certain or possible).

Pass "-" as the transcript to read from stdin.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in, cleanup, err := openInput(cmd, args[0])
			if err != nil {
				return err
			}
			defer cleanup()
			header, err := io.ReadAll(io.LimitReader(in, harness.SniffSize))
			if err != nil {
				return err
			}
			a, conf := agentminutes.Detect(header)
			if conf == harness.NoMatch {
				return fmt.Errorf("unrecognized transcript format")
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", a.ID(), conf)
			return err
		},
	}
}
