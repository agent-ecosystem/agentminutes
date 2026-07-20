package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/harness/codex"
	"github.com/agent-ecosystem/agentminutes/session"
)

// promotions maps --promote flag values onto the opt-in telemetry
// promotion transforms the adapter packages export. Flags are the one
// string boundary; the library API composes the transforms as typed
// functions (see plans/telemetry-promotion.md).
var promotions = map[string]struct {
	Harness   harness.ID
	Transform session.Transform
}{
	"codex:patch-apply": {harness.Codex, codex.PromotePatchApply},
	"codex:web-search":  {harness.Codex, codex.PromoteWebSearch},
}

// promoteFlagHelp lists the known promotion names for flag help.
func promoteFlagHelp() string {
	names := make([]string, 0, len(promotions))
	for n := range promotions {
		names = append(names, `"`+n+`"`)
	}
	sort.Strings(names)
	return fmt.Sprintf("opt-in telemetry promotions to apply (%s); synthesized events are marked promoted_from", strings.Join(names, ", "))
}

// resolvePromotions validates --promote values against the resolved
// harness and returns the transforms to apply, in flag order.
func resolvePromotions(names []string, id harness.ID) ([]session.Transform, error) {
	var transforms []session.Transform
	for _, n := range names {
		p, ok := promotions[n]
		if !ok {
			return nil, fmt.Errorf("unknown promotion %q; %s", n, promoteFlagHelp())
		}
		if p.Harness != id {
			return nil, fmt.Errorf("promotion %q applies to %s transcripts, not %s", n, p.Harness, id)
		}
		transforms = append(transforms, p.Transform)
	}
	return transforms, nil
}
