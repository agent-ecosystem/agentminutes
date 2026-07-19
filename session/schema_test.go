package session

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

// TestFieldProvenance enforces the package invariant that every exported
// field of every schema struct is classified as ACP-mapped, OTel-named, or
// a transcript-only extension via its `schema` tag. This keeps the
// ACP-projection mapping table derivable from the types and unable to rot.
func TestFieldProvenance(t *testing.T) {
	roots := []any{
		Event{},
		Session{},
		ToolInteraction{},
		Stats{},
	}

	valid := map[string]bool{"acp": true, "otel": true, "ext": true}
	pkgPath := reflect.TypeOf(Event{}).PkgPath()
	seen := make(map[reflect.Type]bool)

	var check func(rt reflect.Type)
	check = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice ||
			rt.Kind() == reflect.Array || rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || rt.PkgPath() != pkgPath || seen[rt] {
			return
		}
		seen[rt] = true
		for i := range rt.NumField() {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("schema")
			if !valid[tag] {
				t.Errorf("%s.%s: schema tag %q, want one of acp/otel/ext", rt.Name(), f.Name, tag)
			}
			check(f.Type)
		}
	}

	for _, root := range roots {
		check(reflect.TypeOf(root))
	}

	// The eventPayloads table must pair every payload field of Event with
	// its kind: each entry's kind string must name an Event field's JSON
	// key, and every pointer field of Event other than the non-payload ones
	// (Timestamp, Provenance) must appear in the table. This keeps Validate
	// unable to drift from the struct definition.
	payloadKinds := make(map[string]bool)
	for _, p := range eventPayloads {
		payloadKinds[string(p.kind)] = true
	}
	et := reflect.TypeOf(Event{})
	nonPayload := map[string]bool{"Timestamp": true, "Provenance": true}
	payloadFields := 0
	for i := range et.NumField() {
		f := et.Field(i)
		if f.Type.Kind() != reflect.Pointer || nonPayload[f.Name] {
			continue
		}
		payloadFields++
		jsonName, _, _ := bytes.Cut([]byte(f.Tag.Get("json")), []byte(","))
		if !payloadKinds[string(jsonName)] {
			t.Errorf("Event.%s (json %q) has no eventPayloads entry", f.Name, jsonName)
		}
	}
	if payloadFields != len(eventPayloads) {
		t.Errorf("Event has %d payload fields but eventPayloads has %d entries", payloadFields, len(eventPayloads))
	}

	// Every payload struct must be reachable from Event, or the walk above
	// silently stops covering it. This list is intentionally independent of
	// the struct definitions: it is the copy that gives the test teeth.
	for _, name := range []string{
		"Meta", "UserMessage", "AssistantMessage", "Thinking", "ToolCall",
		"ToolResult", "SystemEvent", "Unknown", "ContentBlock", "TokenUsage",
		"Provenance", "FetchInfo", "Report",
	} {
		found := false
		for rt := range seen {
			if rt.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("schema struct %s not reachable from test roots", name)
		}
	}
}

// TestReadmeSchemaVersion keeps the README's prose mention of the schema
// version paired with the SchemaVersion constant.
func TestReadmeSchemaVersion(t *testing.T) {
	data, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("`"+SchemaVersion+"`")) {
		t.Errorf("README.md does not mention current SchemaVersion %q", SchemaVersion)
	}
}
