// Package summary provides searchable session summaries with vector embeddings.
package summary

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/memory"
)

// Summary represents a vectorized session summary.
type Summary struct {
	ID          string
	SessionID   string
	SummaryText string
	Embedding   []float32
	KeyTopics   []string
	CreatedAt   time.Time
}

// Store provides storage and retrieval for session summaries.
type Store struct {
	queries   db.Querier
	embedder  *memory.EmbeddingClient
	useVector bool
}

// NewStore creates a new summary store.
func NewStore(queries db.Querier, ollamaURL, model string) *Store {
	s := &Store{queries: queries}

	if ollamaURL != "" {
		s.embedder = memory.NewEmbeddingClient(ollamaURL, model)
		// Test connection.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.embedder.Embed(ctx, "test"); err == nil {
			s.useVector = true
		}
	}

	return s
}

// Save stores a session summary and generates its embedding.
func (s *Store) Save(ctx context.Context, sessionID, summaryText string, keyTopics []string) (*Summary, error) {
	id := fmt.Sprintf("sum_%d", time.Now().UnixNano())

	// Generate embedding if available.
	var embeddingBlob []byte
	if s.useVector && s.embedder != nil {
		if emb, err := s.embedder.Embed(ctx, summaryText); err == nil {
			embeddingBlob = memory.SerializeEmbedding(emb)
		}
	}

	// Serialize key topics.
	var topicsJSON []byte
	if len(keyTopics) > 0 {
		topicsJSON, _ = json.Marshal(keyTopics)
	}

	row, err := s.queries.CreateSessionSummary(ctx, db.CreateSessionSummaryParams{
		ID:          id,
		SessionID:   sessionID,
		SummaryText: summaryText,
		Embedding:   embeddingBlob,
		KeyTopics:   sql.NullString{String: string(topicsJSON), Valid: len(topicsJSON) > 0},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create session summary: %w", err)
	}

	return s.rowToSummary(row), nil
}

// Search finds summaries semantically similar to the query.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]Summary, error) {
	if limit <= 0 {
		limit = 10
	}

	if s.useVector && s.embedder != nil {
		return s.semanticSearch(ctx, query, limit)
	}
	return s.textSearch(ctx, query, limit)
}

func (s *Store) semanticSearch(ctx context.Context, query string, limit int) ([]Summary, error) {
	// Embed the query.
	queryEmb, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return s.textSearch(ctx, query, limit)
	}

	// Load all summaries with embeddings.
	rows, err := s.queries.ListSessionSummariesWithEmbeddings(ctx)
	if err != nil {
		return nil, err
	}

	type scored struct {
		summary Summary
		score   float32
	}
	var results []scored

	for _, row := range rows {
		if len(row.Embedding) == 0 {
			continue
		}
		emb := memory.DeserializeEmbedding(row.Embedding)
		score := memory.CosineSimilarity(queryEmb, emb)
		if score > 0.3 { // Threshold
			results = append(results, scored{
				summary: *s.rowToSummary(row),
				score:   score,
			})
		}
	}

	// Sort by similarity (highest first).
	for i := range results {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Return top N.
	summaries := make([]Summary, 0, limit)
	for i := 0; i < len(results) && i < limit; i++ {
		summaries = append(summaries, results[i].summary)
	}
	return summaries, nil
}

func (s *Store) textSearch(ctx context.Context, query string, limit int) ([]Summary, error) {
	// Fall back to listing all and filtering (simple substring match).
	rows, err := s.queries.ListSessionSummaries(ctx)
	if err != nil {
		return nil, err
	}

	var results []Summary
	for _, row := range rows {
		// Simple contains check - could use FTS5 for better performance.
		if containsIgnoreCase(row.SummaryText, query) {
			results = append(results, *s.rowToSummary(row))
			if len(results) >= limit {
				break
			}
		}
	}
	return results, nil
}

// GetBySessionID returns the most recent summary for a session.
func (s *Store) GetBySessionID(ctx context.Context, sessionID string) (*Summary, error) {
	row, err := s.queries.GetSessionSummaryBySessionID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return s.rowToSummary(row), nil
}

// Delete removes a summary by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	return s.queries.DeleteSessionSummary(ctx, id)
}

func (s *Store) rowToSummary(row db.SessionSummary) *Summary {
	sum := &Summary{
		ID:          row.ID,
		SessionID:   row.SessionID,
		SummaryText: row.SummaryText,
		CreatedAt:   time.Unix(row.CreatedAt, 0),
	}

	if len(row.Embedding) > 0 {
		sum.Embedding = memory.DeserializeEmbedding(row.Embedding)
	}

	if row.KeyTopics.Valid && row.KeyTopics.String != "" {
		json.Unmarshal([]byte(row.KeyTopics.String), &sum.KeyTopics)
	}

	return sum
}

func containsIgnoreCase(s, substr string) bool {
	// Simple case-insensitive contains.
	return len(s) >= len(substr) && 
		(substr == "" || 
		 (len(s) > 0 && containsLower(toLower(s), toLower(substr))))
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func containsLower(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// FormatForContext formats summaries for inclusion in agent context.
func FormatForContext(summaries []Summary) string {
	if len(summaries) == 0 {
		return ""
	}

	var result string
	result = "<past_session_summaries>\n"
	for _, sum := range summaries {
		result += fmt.Sprintf("## Session %s\n%s\n\n", sum.SessionID[:8], sum.SummaryText)
	}
	result += "</past_session_summaries>"
	return result
}
