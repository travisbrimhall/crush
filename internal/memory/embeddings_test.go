package memory

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSerializeDeserializeEmbedding(t *testing.T) {
	t.Parallel()

	original := []float32{0.1, 0.2, 0.3, -0.5, 1.0, 0.0}
	serialized := SerializeEmbedding(original)
	require.Len(t, serialized, len(original)*4)

	deserialized := DeserializeEmbedding(serialized)
	require.Equal(t, original, deserialized)

	// Empty input.
	require.Empty(t, DeserializeEmbedding(nil))
	require.Nil(t, DeserializeEmbedding([]byte{1, 2, 3})) // Not divisible by 4.
}

func TestCosineSimilarity(t *testing.T) {
	t.Parallel()

	// Identical vectors should have similarity 1.
	a := []float32{1.0, 0.0, 0.0}
	require.InDelta(t, 1.0, CosineSimilarity(a, a), 0.001)

	// Orthogonal vectors should have similarity 0.
	b := []float32{0.0, 1.0, 0.0}
	require.InDelta(t, 0.0, CosineSimilarity(a, b), 0.001)

	// Opposite vectors should have similarity -1.
	c := []float32{-1.0, 0.0, 0.0}
	require.InDelta(t, -1.0, CosineSimilarity(a, c), 0.001)

	// Mismatched lengths.
	require.Equal(t, float32(0), CosineSimilarity(a, []float32{1.0}))

	// Empty vectors.
	require.Equal(t, float32(0), CosineSimilarity([]float32{}, []float32{}))
}
