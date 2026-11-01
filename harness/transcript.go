package harness

import (
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// CompressedSuffix marks a transcript stored as a zstd stream of its JSONL
// (Codex CLI 0.164.0+ rollout compression writes <rollout>.jsonl.zst; the
// decoded bytes are the plain file, verified against archives the harness
// produced). Adapters and locators read transcripts through OpenTranscript
// or ReadTranscript so the representation is decided in one place.
const CompressedSuffix = ".zst"

// IsCompressedTranscript reports whether path names a compressed transcript.
func IsCompressedTranscript(path string) bool {
	return strings.HasSuffix(path, CompressedSuffix)
}

// OpenTranscript opens a transcript for reading, decoding it transparently
// when it is compressed. Closing the returned reader closes the file.
func OpenTranscript(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !IsCompressedTranscript(path) {
		return f, nil
	}
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &decodedFile{Reader: dec, dec: dec, f: f}, nil
}

// ReadTranscript reads a whole transcript, decoded when compressed.
func ReadTranscript(path string) ([]byte, error) {
	r, err := OpenTranscript(path)
	if err != nil {
		return nil, err
	}
	defer r.Close() //nolint:errcheck // read-only
	return io.ReadAll(r)
}

type decodedFile struct {
	io.Reader
	dec *zstd.Decoder
	f   *os.File
}

func (d *decodedFile) Close() error {
	d.dec.Close()
	return d.f.Close()
}
