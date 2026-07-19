package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/driftprobe"
	"github.com/spf13/cobra"
)

// categoryError converts a non-clean category into the command's exit
// code. The report already said everything, so error printing is silenced
// for this sentinel (genuine errors still print).
func categoryError(cmd *cobra.Command, cat driftprobe.Category) error {
	if code := cat.ExitCode(); code != 0 {
		return silentExit(cmd, code)
	}
	return nil
}

func newDriftCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Check transcripts for harness format drift",
		Long: `Drift checks whether a harness's transcript format has moved past what
this build's adapters were validated against.

scan diffs existing transcripts against the embedded vocabulary baseline
(free, no harness invocation): if the parser gave you unexpected output,
scan the transcript to see whether format drift is the cause.

Maintainer verbs (hidden from help; see DEVELOPMENT.md): probe invokes the
installed harnesses headlessly against fixed tasks and checks the fresh
transcripts, and baseline regenerates the embedded vocabulary files.

Exit codes: 0 clean, 1 drift detected, 2 inconclusive probes, 3 execution
error.`,
	}
	cmd.AddCommand(newDriftScanCmd(), newDriftProbeCmd(), newDriftBaselineCmd())
	return cmd
}

func newDriftScanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan <transcript>...",
		Short: "Diff existing transcripts against the vocabulary baseline (free)",
		Long: `Scan strict-parses each transcript (with line accounting) and diffs its
observed vocabulary (record types, key sets, churn-prone discriminator
values) against the baseline this build was validated with. The harness is
auto-detected per file. Nothing is invoked and nothing is spent.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			worst := driftprobe.Clean
			for _, path := range args {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if cat := driftprobe.Scan(cmd.OutOrStdout(), path, data); cat > worst {
					worst = cat
				}
			}
			return categoryError(cmd, worst)
		},
	}
}

func newDriftProbeCmd() *cobra.Command {
	var (
		harnesses []string
		force     bool
		keep      bool
		timeout   time.Duration
	)
	cmd := &cobra.Command{
		Use:    "probe",
		Short:  "Probe installed harnesses for format drift (maintainer tool; spends tokens)",
		Hidden: true,
		Long: `Probe invokes each installed harness headlessly (with its
permission-skipping flag, in a disposable working directory) against fixed
tasks that exercise every schema shape, then strict-parses the fresh
transcripts and diffs their vocabulary against the baseline.

This spends real tokens/quota on every probed harness. The version gate is
the failsafe: a harness whose installed version equals the last validated
one is skipped unless --force is given.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runners, explicit, err := selectRunners(harnesses)
			if err != nil {
				return err
			}
			cat := driftprobe.RunProbes(cmd.OutOrStdout(), runners, driftprobe.DefaultProbes(), driftprobe.ProbeOptions{
				Force:                force,
				Keep:                 keep,
				Timeout:              timeout,
				MissingBinaryIsError: explicit,
			})
			return categoryError(cmd, cat)
		},
	}
	cmd.Flags().StringSliceVar(&harnesses, "harness", nil, fmt.Sprintf("harnesses to probe (default all: %s)", harnessList()))
	cmd.Flags().BoolVar(&force, "force", false, "probe even when the installed version equals the last validated one")
	cmd.Flags().BoolVar(&keep, "keep", false, "keep copies of the fresh transcripts for inspection")
	cmd.Flags().DurationVar(&timeout, "timeout", driftprobe.DefaultTimeout, "per-invocation timeout")
	return cmd
}

func newDriftBaselineCmd() *cobra.Command {
	var (
		harnessID string
		version   string
		output    string
	)
	cmd := &cobra.Command{
		Use:    "baseline <transcript-or-dir>...",
		Short:  "Regenerate a vocabulary baseline from transcripts (maintainer tool)",
		Hidden: true,
		Long: `Baseline builds the vocabulary JSON for one harness from transcript
files and directories (directories are walked for .jsonl; files that do not
sniff as the harness are skipped). Write it over the embedded file in
internal/driftprobe/baselines/ when reconciling drift, alongside updates to
harness.LastValidated and the format inventory.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := driftprobe.Generate(cmd.ErrOrStderr(), harness.ID(harnessID), version, args)
			if err != nil {
				return err
			}
			return withOutput(output, cmd.OutOrStdout(), func(out io.Writer) error {
				return driftprobe.WriteBaseline(out, b)
			})
		},
	}
	cmd.Flags().StringVar(&harnessID, "harness", "", fmt.Sprintf("harness the transcripts belong to (%s)", harnessList()))
	cmd.Flags().StringVar(&version, "version", "", "harness version the transcripts were generated with (informational)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to a file instead of stdout")
	_ = cmd.MarkFlagRequired("harness")
	return cmd
}

// selectRunners filters the runner table by --harness values; explicit
// reports whether the user named harnesses (missing binaries then error
// rather than skip).
func selectRunners(names []string) ([]driftprobe.Runner, bool, error) {
	all := driftprobe.DefaultRunners()
	if len(names) == 0 {
		return all, false, nil
	}
	var selected []driftprobe.Runner
	for _, name := range names {
		i := slices.IndexFunc(all, func(r driftprobe.Runner) bool { return r.ID == harness.ID(name) })
		if i < 0 {
			return nil, false, fmt.Errorf("unknown harness %q (want %s)", name, knownHarnesses(all))
		}
		selected = append(selected, all[i])
	}
	return selected, true, nil
}

func knownHarnesses(runners []driftprobe.Runner) string {
	ids := make([]string, len(runners))
	for i, r := range runners {
		ids[i] = string(r.ID)
	}
	return strings.Join(ids, ", ")
}
