package similarity

import (
	"bytes"
	"crypto/sha256"
	"io"
	"math/rand"
	"reflect"
	"testing"
	"testing/iotest"
)

func randomInput(size int) []byte {
	data := make([]byte, size)
	rand.New(rand.NewSource(42)).Read(data)
	return data
}

func mustChunk(t *testing.T, r io.Reader) []ChunkRef {
	t.Helper()
	refs, err := Chunk(r)
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

func TestChunkDeterministic(t *testing.T) {
	data := randomInput(4 << 20)
	want := mustChunk(t, bytes.NewReader(data))
	for _, r := range []io.Reader{bytes.NewReader(data), iotest.HalfReader(bytes.NewReader(data)), iotest.DataErrReader(bytes.NewReader(data))} {
		if got := mustChunk(t, r); !reflect.DeepEqual(got, want) {
			t.Fatal("chunk references depend on run or reader segmentation")
		}
	}
}

func TestChunkEmpty(t *testing.T) {
	if got := mustChunk(t, bytes.NewReader(nil)); len(got) != 0 {
		t.Fatalf("empty input produced %d chunks", len(got))
	}
}

func TestChunkInsertionLocality(t *testing.T) {
	data := randomInput(4 << 20)
	middle := len(data) / 2
	modified := make([]byte, 0, len(data)+100)
	modified = append(modified, data[:middle]...)
	modified = append(modified, bytes.Repeat([]byte{0xa5}, 100)...)
	modified = append(modified, data[middle:]...)
	before := mustChunk(t, bytes.NewReader(data))
	after := mustChunk(t, bytes.NewReader(modified))
	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix && before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}
	if prefix == 0 || suffix == 0 {
		t.Fatal("expected unchanged chunks on both sides of insertion")
	}
	if oldChanged, newChanged := len(before)-prefix-suffix, len(after)-prefix-suffix; oldChanged > 4 || newChanged > 4 {
		t.Fatalf("insertion changed %d old and %d new chunks; want at most 4 each", oldChanged, newChanged)
	}
}

func TestChunkSizesAndHashes(t *testing.T) {
	for _, size := range []int{1, minChunkSize - 1, minChunkSize, maxChunkSize, 4 << 20} {
		for _, data := range [][]byte{randomInput(size), make([]byte, size)} {
			refs := mustChunk(t, bytes.NewReader(data))
			offset := 0
			for i, ref := range refs {
				if ref.Size <= 0 || ref.Size > maxChunkSize || (i < len(refs)-1 && ref.Size < minChunkSize) {
					t.Fatalf("input %d, chunk %d: invalid size %d", size, i, ref.Size)
				}
				end := offset + int(ref.Size)
				if end > len(data) {
					t.Fatal("chunks exceed input length")
				}
				digest := sha256.Sum256(data[offset:end])
				if !bytes.Equal(ref.Hash[:], digest[:8]) {
					t.Fatalf("chunk %d hash differs from SHA-256 prefix", i)
				}
				offset = end
			}
			if offset != len(data) {
				t.Fatalf("chunks cover %d bytes, want %d", offset, len(data))
			}
		}
	}
}

func TestChunkReadError(t *testing.T) {
	want := io.ErrUnexpectedEOF
	if _, err := Chunk(iotest.ErrReader(want)); err != want {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
