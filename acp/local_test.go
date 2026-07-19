package acp_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/acp"
	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/harness/claudecode"
)

// TestLocalLossReport projects every real Claude Code transcript under
// AGENTMINUTES_LOCAL_TRANSCRIPTS and aggregates the loss reports: the
// empirical answer to what an ACP adapter would not have seen.
func TestLocalLossReport(t *testing.T) {
	dir := os.Getenv("AGENTMINUTES_LOCAL_TRANSCRIPTS")
	if dir == "" {
		t.Skip("set AGENTMINUTES_LOCAL_TRANSCRIPTS to run against real transcripts")
	}
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			paths = append(paths, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	var events, projected, updates int
	droppedEvents := make(map[string]int)
	droppedFields := make(map[string]int)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s, err := harness.Parse(claudecode.Adapter{}, bytes.NewReader(data), harness.Options{})
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		_, loss := acp.Project(s)
		events += loss.TotalEvents
		projected += loss.ProjectedEvents
		updates += loss.Updates
		for k, n := range loss.DroppedEvents {
			droppedEvents[k] += n
		}
		for k, n := range loss.DroppedFields {
			droppedFields[k] += n
		}
	}
	t.Logf("%d transcripts: %d events -> %d updates (%d events projected, %d dropped)",
		len(paths), events, updates, projected, events-projected)
	t.Logf("dropped events: %v", droppedEvents)
	t.Logf("dropped fields: %v", droppedFields)
}
