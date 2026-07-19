package harness

import (
	"io"

	"github.com/agent-ecosystem/agentminutes/session"
)

// Parse drains a's event stream from r into an accumulated Session, wiring
// skip accounting into the session report. It is the whole-file convenience
// over Adapter.Events; streaming consumers range over Events directly.
// Transforms, when given, are applied to the event stream in order (e.g.
// the opt-in telemetry promotions exported by adapter packages).
func Parse(a Adapter, r io.Reader, opts Options, transforms ...session.Transform) (*session.Session, error) {
	acc := session.NewAccumulator()
	userSkip := opts.OnSkip
	opts.OnSkip = func(line int, recordType string) {
		acc.SkipRecord(recordType)
		if userSkip != nil {
			userSkip(line, recordType)
		}
	}
	events := a.Events(r, opts)
	for _, t := range transforms {
		events = t(events)
	}
	for ev, err := range events {
		if err != nil {
			return nil, err
		}
		if err := acc.Add(ev); err != nil {
			return nil, err
		}
	}
	return acc.Session(), nil
}
