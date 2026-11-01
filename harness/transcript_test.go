package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// writeCompressed writes content as a zstd stream at path (a copy of
// locatetest.WriteCompressedTranscript, which cannot be imported here
// without a cycle).
func writeCompressed(t *testing.T, path, content string) {
	t.Helper()
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenTranscriptDecodesCompressed(t *testing.T) {
	dir := t.TempDir()
	content := "{\"type\":\"session_meta\"}\n{\"type\":\"response_item\"}\n"
	plain := filepath.Join(dir, "rollout-x.jsonl")
	packed := filepath.Join(dir, "rollout-y.jsonl.zst")
	if err := os.WriteFile(plain, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCompressed(t, packed, content)

	for _, path := range []string{plain, packed} {
		got, err := ReadTranscript(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if string(got) != content {
			t.Errorf("%s: read %q, want %q", path, got, content)
		}
	}
	if IsCompressedTranscript(plain) || !IsCompressedTranscript(packed) {
		t.Error("IsCompressedTranscript misclassifies")
	}
	// A .zst that is not a zstd stream fails on read, not silently.
	bad := filepath.Join(dir, "rollout-z.jsonl.zst")
	if err := os.WriteFile(bad, []byte("not zstd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTranscript(bad); err == nil {
		t.Error("ReadTranscript accepted a non-zstd .zst file")
	}
}
