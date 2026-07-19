package driftprobe

import (
	"fmt"
	"io"
	"slices"
	"sort"
)

// Diff is the vocabulary delta between a baseline and freshly observed
// transcripts. New entries are drift; baseline entries the fresh set never
// exercised are informational only (probes cannot exercise everything).
type Diff struct {
	// NewRecordTypes are observed record types absent from the baseline.
	NewRecordTypes []string

	// NewKeys maps a known record type to observed key paths absent from
	// the baseline.
	NewKeys map[string][]string

	// NewDiscriminators maps a known record type to discriminator paths to
	// observed values absent from the baseline.
	NewDiscriminators map[string]map[string][]string

	// Unexercised are baseline record types the fresh transcripts never
	// produced. Informational, never drift.
	Unexercised []string
}

// HasDrift reports whether the delta contains anything beyond unexercised
// baseline entries.
func (d *Diff) HasDrift() bool {
	return len(d.NewRecordTypes) > 0 || len(d.NewKeys) > 0 || len(d.NewDiscriminators) > 0
}

// DiffBaseline compares observed vocabulary against the baseline.
func DiffBaseline(baseline, observed *Baseline) *Diff {
	d := &Diff{
		NewKeys:           map[string][]string{},
		NewDiscriminators: map[string]map[string][]string{},
	}
	for rtype, ov := range observed.RecordTypes {
		bv, known := baseline.RecordTypes[rtype]
		if !known {
			d.NewRecordTypes = append(d.NewRecordTypes, rtype)
			continue
		}
		for _, k := range ov.Keys {
			if !slices.Contains(bv.Keys, k) {
				d.NewKeys[rtype] = append(d.NewKeys[rtype], k)
			}
		}
		for path, values := range ov.Discriminators {
			for _, v := range values {
				if !slices.Contains(bv.Discriminators[path], v) {
					if d.NewDiscriminators[rtype] == nil {
						d.NewDiscriminators[rtype] = map[string][]string{}
					}
					d.NewDiscriminators[rtype][path] = append(d.NewDiscriminators[rtype][path], v)
				}
			}
		}
	}
	for rtype := range baseline.RecordTypes {
		if _, ok := observed.RecordTypes[rtype]; !ok {
			d.Unexercised = append(d.Unexercised, rtype)
		}
	}
	sort.Strings(d.NewRecordTypes)
	sort.Strings(d.Unexercised)
	return d
}

// write renders the delta grouped by severity.
func (d *Diff) write(w io.Writer) {
	for _, rtype := range d.NewRecordTypes {
		say(w, "  drift: new record type %q\n", rtype)
	}
	for _, rtype := range sortedMapKeys(d.NewKeys) {
		for _, k := range d.NewKeys[rtype] {
			say(w, "  drift: record type %q has new key %q\n", rtype, k)
		}
	}
	for _, rtype := range sortedMapKeys(d.NewDiscriminators) {
		for _, path := range sortedMapKeys(d.NewDiscriminators[rtype]) {
			for _, v := range d.NewDiscriminators[rtype][path] {
				say(w, "  drift: record type %q has new %s value %q\n", rtype, path, v)
			}
		}
	}
	if len(d.Unexercised) > 0 {
		say(w, "  info: baseline record types not exercised: %v\n", d.Unexercised)
	}
}

// say writes one report line; report output ignores writer errors, like
// the rest of the CLI's stderr/stdout reporting.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
