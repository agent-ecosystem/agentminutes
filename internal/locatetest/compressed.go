package locatetest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// WriteCompressedTranscript writes content at path as a zstd stream, the
// representation Codex's rollout compression produces (<rollout>.jsonl.zst
// decoding to the plain file), for locator and reader tests. Fixtures stay
// plain text in the repository; the archive is generated per test.
func WriteCompressedTranscript(t *testing.T, path, content string) {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
