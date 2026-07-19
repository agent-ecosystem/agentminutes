package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-ecosystem/agentminutes"
	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/spf13/cobra"
)

func newSessionsCmd() *cobra.Command {
	var (
		cwd       string
		format    string
		harnessID string
		root      string
		sessionID string
		since     string
		until     string
	)
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List native session transcripts, filtered by identity or time",
		Long: `Sessions discovers native transcripts in the harnesses' own log
directories (all default roots unless --harness narrows it) and prints
them. The default paths format prints one transcript path per line,
subagent transcripts included, so the output composes:

  agentminutes sessions --cwd ~/runs/exp-42 | xargs -n1 agentminutes convert

Sessions with no recorded cwd (Antigravity records none) never match
--cwd; the summary counts them. Per-file scan errors go to stderr and set
exit status 1 without aborting the scan; zero matches is an answer, not a
failure.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if root != "" && harnessID == "" {
				return fmt.Errorf("--root requires --harness")
			}
			sinceT, err := parseWhen(since, false)
			if err != nil {
				return err
			}
			untilT, err := parseWhen(until, true)
			if err != nil {
				return err
			}
			var skips int
			opts := harness.ScanOptions{
				Since:  sinceT,
				Until:  untilT,
				OnSkip: func(string, string) { skips++ },
			}
			refs, err := sessionsSource(harnessID, root, sessionID, opts)
			if err != nil {
				return err
			}
			out := newSessionsOutput(cmd.OutOrStdout(), format)
			if out == nil {
				return fmt.Errorf("unknown format %q (want paths, json, or jsonl)", format)
			}
			var matched, filtered, noCWD, errs int
			for ref, err := range refs {
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "agentminutes: %v\n", err)
					errs++
					continue
				}
				if !opts.Includes(ref.StartedAt, ref.ModTime) {
					filtered++ // Locate bypasses ScanOptions; re-check
					continue
				}
				if sessionID != "" && ref.Meta.SessionID != sessionID {
					filtered++
					continue
				}
				if cwd != "" {
					if ref.Meta.CWD == "" {
						filtered++
						noCWD++
						continue
					}
					if filepath.Clean(ref.Meta.CWD) != filepath.Clean(cwd) {
						filtered++
						continue
					}
				}
				matched++
				if err := out.ref(ref); err != nil {
					return err
				}
			}
			if err := out.close(); err != nil {
				return err
			}
			summary := fmt.Sprintf("agentminutes: %s, %d filtered out", plural(matched, "session"), filtered)
			if noCWD > 0 {
				summary += fmt.Sprintf(" (%d without recorded cwd)", noCWD)
			}
			summary += fmt.Sprintf(", %s, %s", plural(skips, "skipped file"), plural(errs, "error"))
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), summary)
			if errs > 0 {
				// The per-file errors already printed; exit 1 without
				// cobra appending its own error line.
				return silentExit(cmd, 1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cwd, "cwd", "", "keep sessions whose recorded working directory equals this path")
	cmd.Flags().StringVar(&format, "format", "paths", "output format: paths (one transcript path per line), json, or jsonl")
	cmd.Flags().StringVar(&harnessID, "harness", "", fmt.Sprintf("restrict to one harness (%s); default scans all default roots", harnessList()))
	cmd.Flags().StringVar(&root, "root", "", "explicit transcript root to scan (requires --harness)")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "keep the session with this ID (resolved directly when --harness is also given)")
	cmd.Flags().StringVar(&since, "since", "", "keep sessions active at or after this time (RFC 3339, or YYYY-MM-DD for start of that local day)")
	cmd.Flags().StringVar(&until, "until", "", "keep sessions active at or before this time (RFC 3339, or YYYY-MM-DD for end of that local day)")
	return cmd
}

// sessionsSource picks the ref iterator for the flag combination: a direct
// Locate when both harness and session ID are known, one locator's scan
// when the harness is named, the all-roots facade scan otherwise.
func sessionsSource(harnessID, root, sessionID string, opts harness.ScanOptions) (iter.Seq2[harness.SessionRef, error], error) {
	if harnessID == "" {
		return agentminutes.Scan(opts), nil
	}
	l, err := agentminutes.LocatorFor(harness.ID(harnessID))
	if err != nil {
		return nil, err
	}
	explicitRoot := root != ""
	if !explicitRoot {
		if root, err = l.DefaultRoot(); err != nil {
			return nil, err
		}
	}
	if sessionID != "" {
		return func(yield func(harness.SessionRef, error) bool) {
			ref, err := l.Locate(root, sessionID)
			if errors.Is(err, os.ErrNotExist) {
				return // zero matches, not a failure
			}
			yield(ref, err)
		}, nil
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) && !explicitRoot {
			// This machine doesn't run that harness; empty is the answer.
			return func(func(harness.SessionRef, error) bool) {}, nil
		}
		return nil, err
	}
	return l.Scan(root, opts), nil
}

// plural renders "1 session" / "3 sessions" for summary lines.
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// parseWhen parses a --since/--until value. A bare date means the start of
// that day, or its end for the until bound.
func parseWhen(s string, endOfDay bool) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		if endOfDay {
			t = t.Add(24 * time.Hour)
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid time %q (want RFC 3339 or YYYY-MM-DD)", s)
}

// sessionsOutput streams refs in one of the output formats.
type sessionsOutput struct {
	w    io.Writer
	mode string
	refs []harness.SessionRef // json mode buffers for one indented array
	enc  *json.Encoder        // jsonl mode
}

func newSessionsOutput(w io.Writer, format string) *sessionsOutput {
	switch format {
	case "paths", "json", "jsonl":
		o := &sessionsOutput{w: w, mode: format}
		if format == "jsonl" {
			o.enc = json.NewEncoder(w)
		}
		return o
	default:
		return nil
	}
}

func (o *sessionsOutput) ref(ref harness.SessionRef) error {
	switch o.mode {
	case "paths":
		lines := append([]string{ref.Path}, ref.SubagentPaths...)
		_, err := fmt.Fprintln(o.w, strings.Join(lines, "\n"))
		return err
	case "jsonl":
		return o.enc.Encode(ref)
	default:
		o.refs = append(o.refs, ref)
		return nil
	}
}

func (o *sessionsOutput) close() error {
	if o.mode != "json" {
		return nil
	}
	if o.refs == nil {
		o.refs = []harness.SessionRef{}
	}
	enc := json.NewEncoder(o.w)
	enc.SetIndent("", "  ")
	return enc.Encode(o.refs)
}
