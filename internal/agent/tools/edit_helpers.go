package tools

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
)

// findClosestMatch finds the substring in content that is most similar to target.
// Returns the best match and its starting line number (1-based), or empty string
// and 0 if no reasonable match is found.
func findClosestMatch(content, target string) (match string, lineNum int) {
	if content == "" || target == "" {
		return "", 0
	}

	targetLines := strings.Split(target, "\n")
	contentLines := strings.Split(content, "\n")
	targetLineCount := len(targetLines)

	if targetLineCount > len(contentLines) {
		return "", 0
	}

	var bestMatch string
	var bestScore float64
	var bestLineNum int

	// Slide a window of targetLineCount lines across the content.
	for i := 0; i <= len(contentLines)-targetLineCount; i++ {
		candidate := strings.Join(contentLines[i:i+targetLineCount], "\n")
		score := similarityScore(target, candidate)

		if score > bestScore {
			bestScore = score
			bestMatch = candidate
			bestLineNum = i + 1
		}
	}

	// Only return a match if it's reasonably similar (> 50%).
	if bestScore < 0.5 {
		return "", 0
	}

	return bestMatch, bestLineNum
}

// similarityScore calculates a simple similarity ratio between two strings.
// Returns a value between 0 (no similarity) and 1 (identical).
func similarityScore(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	// Use longest common subsequence ratio.
	lcsLen := longestCommonSubsequence(a, b)
	return float64(2*lcsLen) / float64(len(a)+len(b))
}

// longestCommonSubsequence returns the length of the LCS of two strings.
func longestCommonSubsequence(a, b string) int {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return 0
	}

	// Optimize space by only keeping two rows.
	prev := make([]int, n+1)
	curr := make([]int, n+1)

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1] + 1
			} else {
				curr[j] = max(prev[j], curr[j-1])
			}
		}
		prev, curr = curr, prev
	}

	return prev[n]
}

// normalizeWhitespace normalizes whitespace for fuzzy matching:
// - Converts tabs to spaces.
// - Trims trailing whitespace from each line.
// - Normalizes multiple spaces to single space (except leading indentation).
func normalizeWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		// Convert tabs to 4 spaces.
		line = strings.ReplaceAll(line, "\t", "    ")
		// Trim trailing whitespace.
		line = strings.TrimRight(line, " \t")
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// generateDiffHint creates a helpful error message showing the difference
// between what was searched for and the closest match found.
func generateDiffHint(target, closestMatch string, lineNum int) string {
	if closestMatch == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n\nClosest match found at line %d:\n", lineNum))

	targetLines := strings.Split(target, "\n")
	matchLines := strings.Split(closestMatch, "\n")

	// Show a side-by-side comparison of differing lines.
	for i := 0; i < len(targetLines) && i < len(matchLines); i++ {
		if targetLines[i] != matchLines[i] {
			sb.WriteString(fmt.Sprintf("  Line %d differs:\n", lineNum+i))
			sb.WriteString(fmt.Sprintf("    Expected: %q\n", truncateLine(targetLines[i], 80)))
			sb.WriteString(fmt.Sprintf("    Found:    %q\n", truncateLine(matchLines[i], 80)))
		}
	}

	return sb.String()
}

// truncateLine truncates a line to maxLen characters, adding ellipsis if needed.
func truncateLine(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// generateLineHash creates a short 3-character hash for a line of code.
// The hash is deterministic based on line number and content.
func generateLineHash(lineNum int, content string) string {
	// Hash the line number and content together for uniqueness.
	data := fmt.Sprintf("%d:%s", lineNum, content)
	hash := sha256.Sum256([]byte(data))
	// Use base32 encoding (alphanumeric, no ambiguous chars) and take first 3.
	encoded := base32.StdEncoding.EncodeToString(hash[:])
	return strings.ToLower(encoded[:3])
}

// addLineNumbersWithHashes formats content with line numbers and short hashes.
// Format: "  linenum|hash|content"
func addLineNumbersWithHashes(content string, startLine int) string {
	if content == "" {
		return ""
	}

	lines := strings.Split(content, "\n")
	result := make([]string, len(lines))

	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		lineNum := i + startLine
		hash := generateLineHash(lineNum, line)
		numStr := fmt.Sprintf("%d", lineNum)

		if len(numStr) >= 6 {
			result[i] = fmt.Sprintf("%s|%s|%s", numStr, hash, line)
		} else {
			paddedNum := fmt.Sprintf("%6s", numStr)
			result[i] = fmt.Sprintf("%s|%s|%s", paddedNum, hash, line)
		}
	}

	return strings.Join(result, "\n")
}

// generateNotFoundError creates a helpful error message when old_string is not
// found. It attempts to find the closest match and shows the differences.
func generateNotFoundError(content, target string) string {
	baseMsg := "old_string not found in file. Make sure it matches exactly, including whitespace and line breaks."

	closestMatch, lineNum := findClosestMatch(content, target)
	if closestMatch == "" {
		return baseMsg
	}

	hint := generateDiffHint(target, closestMatch, lineNum)
	return baseMsg + hint
}

// findFuzzyIndex finds the position of target in content using fuzzy whitespace
// matching. Returns the byte index or -1 if not found.
func findFuzzyIndex(content, target string) int {
	normalizedContent := normalizeWhitespace(content)
	normalizedTarget := normalizeWhitespace(target)

	normalizedIndex := strings.Index(normalizedContent, normalizedTarget)
	if normalizedIndex == -1 {
		return -1
	}

	// Map the normalized index back to the original content.
	// We do this by counting characters up to the normalized index.
	contentLines := strings.Split(content, "\n")
	normalizedLines := strings.Split(normalizedContent, "\n")

	// Find which line the match starts on.
	charCount := 0
	matchLineIdx := 0
	for i, line := range normalizedLines {
		lineLen := len(line) + 1 // +1 for newline
		if charCount+lineLen > normalizedIndex {
			matchLineIdx = i
			break
		}
		charCount += lineLen
	}

	// Calculate the offset within the original content.
	originalCharCount := 0
	for i := 0; i < matchLineIdx; i++ {
		originalCharCount += len(contentLines[i]) + 1
	}

	// Add the offset within the line (accounting for whitespace differences).
	normalizedLineOffset := normalizedIndex - charCount
	originalLine := contentLines[matchLineIdx]
	normalizedLine := normalizedLines[matchLineIdx]

	// Find corresponding position in original line.
	if normalizedLineOffset < len(normalizedLine) {
		// Simple approach: find the target start in the original line.
		targetFirstLine := strings.Split(target, "\n")[0]
		lineIndex := strings.Index(originalLine, strings.TrimLeft(targetFirstLine, " \t"))
		if lineIndex != -1 {
			// Account for leading whitespace.
			leadingWS := len(targetFirstLine) - len(strings.TrimLeft(targetFirstLine, " \t"))
			return originalCharCount + lineIndex - leadingWS
		}
	}

	return originalCharCount + normalizedLineOffset
}

// fuzzyReplaceAll replaces all occurrences of target in content using fuzzy
// whitespace matching. Returns the modified content.
func fuzzyReplaceAll(content, target, replacement string) string {
	normalizedContent := normalizeWhitespace(content)
	normalizedTarget := normalizeWhitespace(target)

	// If there's no match even with normalization, return original.
	if !strings.Contains(normalizedContent, normalizedTarget) {
		return content
	}

	// For each occurrence in normalized content, find and replace in original.
	result := content
	for {
		idx := findFuzzyIndex(result, target)
		if idx == -1 {
			break
		}
		result = result[:idx] + replacement + result[idx+len(target):]
	}

	return result
}
