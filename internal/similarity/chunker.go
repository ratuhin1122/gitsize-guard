// Package similarity provides content-based file comparison primitives.
package similarity

import (
	"crypto/sha256"
	"io"
)

const (
	minChunkSize = 16 << 10
	maxChunkSize = 256 << 10
	boundaryMask = (1 << 16) - 1
)

// ChunkRef identifies a chunk by its size and the first eight bytes of SHA-256.
type ChunkRef struct {
	Hash [8]byte
	Size int64
}

// A fixed seed and unsigned arithmetic make the Gear table platform independent.
var gearTable = makeGearTable()

func makeGearTable() [256]uint64 {
	var table [256]uint64
	state := uint64(0x6a09e667f3bcc909)
	for i := range table {
		// SplitMix64.
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		table[i] = z ^ (z >> 31)
	}
	return table
}

// Chunk splits r at content-defined Gear hash boundaries. Chunks contain at
// least 16 KiB and at most 256 KiB, except that the final chunk may be smaller.
// A 16-bit boundary mask gives roughly 64 KiB between eligible boundaries.
// Input buffering is bounded by the maximum chunk size; returned references
// occupy additional space proportional to the number of chunks. On a read
// error, only completed chunks are returned, together with the error.
func Chunk(r io.Reader) ([]ChunkRef, error) {
	var refs []ChunkRef
	var input [32 << 10]byte
	data := make([]byte, 0, maxChunkSize)
	var rolling uint64
	emit := func() {
		digest := sha256.Sum256(data)
		ref := ChunkRef{Size: int64(len(data))}
		copy(ref.Hash[:], digest[:8])
		refs = append(refs, ref)
		data = data[:0]
		rolling = 0
	}
	emptyReads := 0
	for {
		n, err := r.Read(input[:])
		for _, b := range input[:n] {
			data = append(data, b)
			rolling = (rolling << 1) + gearTable[b]
			if len(data) >= minChunkSize && (rolling&boundaryMask == 0 || len(data) == maxChunkSize) {
				emit()
			}
		}
		if err == io.EOF {
			if len(data) > 0 {
				emit()
			}
			return refs, nil
		}
		if err != nil {
			return refs, err
		}
		if n == 0 {
			emptyReads++
			if emptyReads >= 100 {
				return refs, io.ErrNoProgress
			}
		} else {
			emptyReads = 0
		}
	}
}
