package similarity

// Result describes how much of the new file is shared with the old file.
type Result struct {
	SharedBytes int64
	NewSize     int64
	Similarity  float64
}

// Compare matches chunks by hash, consuming one old occurrence per match.
// Similarity is the matched bytes divided by the new file's total size, or
// zero for an empty new file. Inputs are expected to come from Chunk.
func Compare(newChunks, oldChunks []ChunkRef) Result {
	counts := make(map[[8]byte]int, len(oldChunks))
	for _, chunk := range oldChunks {
		counts[chunk.Hash]++
	}
	var result Result
	for _, chunk := range newChunks {
		result.NewSize += chunk.Size
		if counts[chunk.Hash] > 0 {
			result.SharedBytes += chunk.Size
			counts[chunk.Hash]--
		}
	}
	if result.NewSize != 0 {
		result.Similarity = float64(result.SharedBytes) / float64(result.NewSize)
	}
	return result
}
