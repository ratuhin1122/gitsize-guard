package similarity

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestCompareFiles(t *testing.T) {
	original := randomInput(4 << 20)
	unrelated := make([]byte, len(original))
	rand.New(rand.NewSource(99)).Read(unrelated)
	modified := bytes.Clone(original)
	// Replace a contiguous 5% region; scattered edits can touch every chunk.
	start := len(modified) / 2
	for i := start; i < start+len(modified)/20; i++ {
		modified[i] ^= 0xff
	}
	for _, tc := range []struct {
		name     string
		new, old []byte
		min, max float64
	}{
		{"identical", original, original, 1, 1},
		{"unrelated", unrelated, original, 0, 0.01},
		{"five percent changed", modified, original, 0.9, 0.98},
		{"both empty", nil, nil, 0, 0},
		{"new empty", nil, original, 0, 0},
		{"old empty", original, nil, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newChunks := mustChunk(t, bytes.NewReader(tc.new))
			oldChunks := mustChunk(t, bytes.NewReader(tc.old))
			got := Compare(newChunks, oldChunks)
			if got.NewSize != int64(len(tc.new)) {
				t.Fatalf("NewSize = %d, want %d", got.NewSize, len(tc.new))
			}
			if got.Similarity < tc.min || got.Similarity > tc.max {
				t.Errorf("Similarity = %f, want [%f, %f]", got.Similarity, tc.min, tc.max)
			}
			if got.SharedBytes < 0 || got.SharedBytes > got.NewSize {
				t.Errorf("SharedBytes = %d, outside [0, %d]", got.SharedBytes, got.NewSize)
			}
		})
	}
}

func TestCompareMultiplicityAndByteWeighting(t *testing.T) {
	a := ChunkRef{Hash: [8]byte{1}, Size: 10}
	b := ChunkRef{Hash: [8]byte{2}, Size: 30}
	for _, tc := range []struct {
		name     string
		new, old []ChunkRef
		want     Result
	}{
		{"old occurrence used once", []ChunkRef{a, a, a, b}, []ChunkRef{a}, Result{10, 60, 1.0 / 6}},
		{"two old occurrences", []ChunkRef{a, a, a, b}, []ChunkRef{a, a}, Result{20, 60, 1.0 / 3}},
		{"extra old occurrences", []ChunkRef{a}, []ChunkRef{a, a}, Result{10, 10, 1}},
		{"weighted by bytes", []ChunkRef{a, b}, []ChunkRef{b}, Result{30, 40, 0.75}},
		{"order independent", []ChunkRef{b, a}, []ChunkRef{a, b}, Result{40, 40, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Compare(tc.new, tc.old); got != tc.want {
				t.Errorf("Compare = %+v, want %+v", got, tc.want)
			}
		})
	}
}
