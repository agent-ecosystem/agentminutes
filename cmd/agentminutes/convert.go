package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/agent-ecosystem/agentminutes"
	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/session"
	"github.com/spf13/cobra"
)

// parseFlags are the transcript-parsing flags convert and stats share.
type parseFlags struct {
	harnessID      string
	harnessVersion string
	permissive     bool
	promote        []string
}

func (f *parseFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.harnessID, "harness", "auto", fmt.Sprintf(`source harness (%s, or "auto" to sniff)`, harnessList()))
	cmd.Flags().StringVar(&f.harnessVersion, "harness-version", "", "harness version that wrote the transcript, for formats that don't record one (metadata only; never changes parsing)")
	cmd.Flags().BoolVar(&f.permissive, "permissive", false, "preserve unclassifiable records as unknown events instead of failing")
	cmd.Flags().StringSliceVar(&f.promote, "promote", nil, promoteFlagHelp())
}

// resolve opens the transcript argument, picks the adapter (sniffing when
// --harness is auto), and resolves the requested promotions. The returned
// reader still includes the sniffed header; cleanup is non-nil exactly when
// err is nil.
func (f *parseFlags) resolve(cmd *cobra.Command, arg string) (harness.Adapter, *bufio.Reader, []session.Transform, func(), error) {
	in, cleanup, err := openInput(cmd, arg)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	br := bufio.NewReaderSize(in, harness.SniffSize)
	adapter, err := resolveAdapter(f.harnessID, br)
	if err != nil {
		cleanup()
		return nil, nil, nil, nil, err
	}
	transforms, err := resolvePromotions(f.promote, adapter.ID())
	if err != nil {
		cleanup()
		return nil, nil, nil, nil, err
	}
	return adapter, br, transforms, cleanup, nil
}

func newConvertCmd() *cobra.Command {
	var (
		pf         parseFlags
		format     string
		output     string
		keepRaw    bool
		maxPayload int64
	)
	cmd := &cobra.Command{
		Use:   "convert <transcript>",
		Short: "Convert a native session transcript to the unified schema",
		Long: `Convert parses a harness's native session transcript and emits the
normalized record: json is the whole accumulated session (meta, events,
totals, report); jsonl streams one event per line, with the same per-type
skip accounting as the json report summarized on stderr.

Pass "-" as the transcript to read from stdin.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "json" && format != "jsonl" {
				// Validated before any -o file is created or truncated.
				return fmt.Errorf("unknown format %q (want json or jsonl)", format)
			}
			adapter, br, transforms, cleanup, err := pf.resolve(cmd, args[0])
			if err != nil {
				return err
			}
			defer cleanup()
			opts := harness.Options{
				Permissive:         pf.permissive,
				KeepRaw:            keepRaw,
				MaxPayloadBytes:    maxPayload,
				HarnessVersionHint: pf.harnessVersion,
			}
			return withOutput(output, cmd.OutOrStdout(), func(out io.Writer) error {
				return convert(adapter, br, out, cmd.ErrOrStderr(), format, opts, transforms)
			})
		},
	}
	pf.register(cmd)
	cmd.Flags().StringVar(&format, "format", "json", "output format: json (whole session) or jsonl (event stream)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to a file instead of stdout")
	cmd.Flags().BoolVar(&keepRaw, "keep-raw", false, "retain verbatim source records in event provenance")
	cmd.Flags().Int64Var(&maxPayload, "max-payload-bytes", 0, "replace tool-result payloads larger than this with size+digest placeholders (0 = keep whole)")
	return cmd
}

func convert(adapter harness.Adapter, in io.Reader, out, errOut io.Writer, format string, opts harness.Options, transforms []session.Transform) error {
	switch format {
	case "json":
		s, err := harness.Parse(adapter, in, opts, transforms...)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	case "jsonl":
		var events int
		skipped := map[string]int{}
		opts.OnSkip = func(_ int, recordType string) { skipped[recordType]++ }
		enc := json.NewEncoder(out)
		stream := adapter.Events(in, opts)
		for _, t := range transforms {
			stream = t(stream)
		}
		for ev, err := range stream {
			if err != nil {
				return err
			}
			if err := enc.Encode(ev); err != nil {
				return err
			}
			events++
		}
		_, _ = fmt.Fprintf(errOut, "agentminutes: %d events, %s\n", events, skipSummary(skipped))
		return nil
	default:
		return fmt.Errorf("unknown format %q (want json or jsonl)", format)
	}
}

// skipSummary renders per-type skip accounting, so the jsonl stream carries
// the same breakdown as the json report's skipped_records: a jsonl consumer
// can assert that only documented-skip-list types were dropped without
// re-running in json mode.
func skipSummary(skipped map[string]int) string {
	total := 0
	for _, n := range skipped {
		total += n
	}
	if total == 0 {
		return "0 skipped records"
	}
	types := make([]string, 0, len(skipped))
	for t := range skipped {
		types = append(types, t)
	}
	sort.Strings(types)
	parts := make([]string, len(types))
	for i, t := range types {
		parts[i] = fmt.Sprintf("%s %d", t, skipped[t])
	}
	return fmt.Sprintf("%d skipped records (%s)", total, strings.Join(parts, ", "))
}

// resolveAdapter picks the adapter from an explicit --harness value, or
// sniffs the input's opening bytes when set to auto.
func resolveAdapter(id string, br *bufio.Reader) (harness.Adapter, error) {
	if id != "" && id != "auto" {
		return agentminutes.AdapterFor(harness.ID(id))
	}
	header, err := br.Peek(harness.SniffSize)
	if len(header) == 0 {
		if err != nil && err != io.EOF {
			return nil, err
		}
		return nil, fmt.Errorf("empty input")
	}
	a, conf := agentminutes.Detect(header)
	if conf == harness.NoMatch {
		return nil, fmt.Errorf("could not detect the source harness; pass --harness explicitly")
	}
	return a, nil
}
