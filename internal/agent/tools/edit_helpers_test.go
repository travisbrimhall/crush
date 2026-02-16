package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindClosestMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		content       string
		target        string
		wantMatch     bool
		wantLineNum   int
		minSimilarity float64
	}{
		{
			name:        "exact match",
			content:     "line 1\nline 2\nline 3",
			target:      "line 2",
			wantMatch:   true,
			wantLineNum: 2,
		},
		{
			name:        "whitespace difference",
			content:     "func foo() {\n    return nil\n}",
			target:      "func foo() {\n  return nil\n}",
			wantMatch:   true,
			wantLineNum: 1,
		},
		{
			name:        "no reasonable match",
			content:     "completely different content",
			target:      "func bar() { return 42 }",
			wantMatch:   false,
			wantLineNum: 0,
		},
		{
			name:        "empty content",
			content:     "",
			target:      "something",
			wantMatch:   false,
			wantLineNum: 0,
		},
		{
			name:        "empty target",
			content:     "some content",
			target:      "",
			wantMatch:   false,
			wantLineNum: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, lineNum := findClosestMatch(tt.content, tt.target)
			if tt.wantMatch {
				require.NotEmpty(t, match, "expected a match")
				require.Equal(t, tt.wantLineNum, lineNum)
			} else {
				require.Empty(t, match, "expected no match")
				require.Equal(t, 0, lineNum)
			}
		})
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "tabs to spaces",
			input: "\tfoo",
			want:  "    foo",
		},
		{
			name:  "trailing whitespace removed",
			input: "foo   ",
			want:  "foo",
		},
		{
			name:  "mixed tabs and trailing",
			input: "\tbar  \t",
			want:  "    bar",
		},
		{
			name:  "multiline",
			input: "line 1  \n\tline 2\nline 3   ",
			want:  "line 1\n    line 2\nline 3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeWhitespace(tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGenerateLineHash(t *testing.T) {
	t.Parallel()

	// Hashes should be deterministic.
	hash1 := generateLineHash(1, "func main() {")
	hash2 := generateLineHash(1, "func main() {")
	require.Equal(t, hash1, hash2)

	// Different content should produce different hashes.
	hash3 := generateLineHash(1, "func other() {")
	require.NotEqual(t, hash1, hash3)

	// Different line numbers should produce different hashes.
	hash4 := generateLineHash(2, "func main() {")
	require.NotEqual(t, hash1, hash4)

	// Hash should be 3 characters.
	require.Len(t, hash1, 3)

	// Hash should be lowercase alphanumeric.
	for _, c := range hash1 {
		require.True(t, (c >= 'a' && c <= 'z') || (c >= '2' && c <= '7'),
			"hash should be lowercase base32: %c", c)
	}
}

func TestAddLineNumbersWithHashes(t *testing.T) {
	t.Parallel()

	content := "line 1\nline 2\nline 3"
	result := addLineNumbersWithHashes(content, 1)

	lines := strings.Split(result, "\n")
	require.Len(t, lines, 3)

	// Each line should have format: "  num|hash|content"
	for i, line := range lines {
		parts := strings.SplitN(line, "|", 3)
		require.Len(t, parts, 3, "line %d should have 3 parts: %s", i+1, line)
		require.Len(t, strings.TrimSpace(parts[1]), 3, "hash should be 3 chars")
	}
}

func TestGenerateNotFoundError(t *testing.T) {
	t.Parallel()

	content := "func foo() {\n    return nil\n}"
	target := "func foo() {\n  return nil\n}"

	errMsg := generateNotFoundError(content, target)

	require.Contains(t, errMsg, "old_string not found")
	require.Contains(t, errMsg, "Closest match found")
	require.Contains(t, errMsg, "differs")
}

func TestSimilarityScore(t *testing.T) {
	t.Parallel()

	// Identical strings.
	require.Equal(t, 1.0, similarityScore("hello", "hello"))

	// Completely different.
	score := similarityScore("abc", "xyz")
	require.Less(t, score, 0.5)

	// Similar strings.
	score = similarityScore("hello world", "hello worlb")
	require.Greater(t, score, 0.8)

	// Empty strings.
	require.Equal(t, 0.0, similarityScore("", "hello"))
	require.Equal(t, 0.0, similarityScore("hello", ""))
}
