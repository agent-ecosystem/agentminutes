package driftprobe

import (
	"embed"
	"encoding/json"
	"fmt"

	"github.com/agent-ecosystem/agentminutes/harness"
)

// Baselines are embedded so an installed binary can scan without the repo.
// Regenerate with `agentminutes drift baseline` after reconciling drift
// (see plans/drift-probe-design.md for the checklist).
//
//go:embed baselines/*.json
var baselineFS embed.FS

// LoadBaseline returns the embedded baseline for a harness.
func LoadBaseline(id harness.ID) (*Baseline, error) {
	data, err := baselineFS.ReadFile("baselines/" + string(id) + ".json")
	if err != nil {
		return nil, fmt.Errorf("no embedded baseline for harness %q: %w", id, err)
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("embedded baseline for %q: %w", id, err)
	}
	return &b, nil
}
