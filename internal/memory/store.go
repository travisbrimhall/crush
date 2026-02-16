// Package memory provides persistent cross-session memory for the agent.
// This allows the agent to learn preferences, remember decisions, and
// accumulate knowledge about the user and codebase over time.
package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Entry represents a single memory entry.
type Entry struct {
	ID        string    `json:"id"`
	Category  string    `json:"category"` // e.g., "preference", "learning", "fact", "decision"
	Content   string    `json:"content"`  // The actual memory content
	Source    string    `json:"source"`   // Where this came from (session ID, file, etc.)
	Embedding []float32 `json:"-"`        // Vector embedding for semantic search
	CreatedAt time.Time `json:"created_at"`
}

// Store provides persistent storage for agent memories.
type Store struct {
	db        *sql.DB
	embedder  *EmbeddingClient
	useVector bool // Whether vector search is available
}

// NewStore creates a new memory store using the provided database connection.
func NewStore(db *sql.DB) (*Store, error) {
	return NewStoreWithEmbeddings(db, "", "")
}

// NewStoreWithEmbeddings creates a memory store with vector embedding support.
// If ollamaURL is empty, vector search is disabled.
func NewStoreWithEmbeddings(db *sql.DB, ollamaURL, model string) (*Store, error) {
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate memory store: %w", err)
	}

	// Try to initialize embeddings if URL provided.
	if ollamaURL != "" {
		s.embedder = NewEmbeddingClient(ollamaURL, model)
		// Test connection by embedding a short string.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.embedder.Embed(ctx, "test"); err == nil {
			s.useVector = true
		}
	}

	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			category TEXT NOT NULL,
			content TEXT NOT NULL,
			source TEXT,
			embedding BLOB,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_memories_category ON memories(category);
		CREATE INDEX IF NOT EXISTS idx_memories_created_at ON memories(created_at);
	`)
	return err
}

// Save stores a new memory entry.
func (s *Store) Save(ctx context.Context, entry Entry) error {
	if entry.ID == "" {
		entry.ID = generateID()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	// Generate embedding if available.
	var embeddingBlob []byte
	if s.useVector && s.embedder != nil {
		if emb, err := s.embedder.Embed(ctx, entry.Content); err == nil {
			embeddingBlob = SerializeEmbedding(emb)
		}
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memories (id, category, content, source, embedding, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			category = excluded.category,
			content = excluded.content,
			source = excluded.source,
			embedding = excluded.embedding
	`, entry.ID, entry.Category, entry.Content, entry.Source, embeddingBlob, entry.CreatedAt)
	return err
}

// List returns all memories, optionally filtered by category.
func (s *Store) List(ctx context.Context, category string) ([]Entry, error) {
	var rows *sql.Rows
	var err error

	if category != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, category, content, source, created_at
			FROM memories
			WHERE category = ?
			ORDER BY created_at DESC
		`, category)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, category, content, source, created_at
			FROM memories
			ORDER BY created_at DESC
		`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Category, &e.Content, &e.Source, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Search returns memories matching the query.
// If vector search is available, uses semantic similarity.
// Otherwise falls back to substring matching.
func (s *Store) Search(ctx context.Context, query string) ([]Entry, error) {
	if s.useVector && s.embedder != nil {
		return s.semanticSearch(ctx, query, 20)
	}
	return s.substringSearch(ctx, query)
}

// SemanticSearch finds memories similar to the query using embeddings.
func (s *Store) semanticSearch(ctx context.Context, query string, limit int) ([]Entry, error) {
	// Embed the query.
	queryEmb, err := s.embedder.Embed(ctx, query)
	if err != nil {
		// Fall back to substring search.
		return s.substringSearch(ctx, query)
	}

	// Load all memories with embeddings.
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, category, content, source, embedding, created_at
		FROM memories
		WHERE embedding IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		entry Entry
		score float32
	}
	var results []scored

	for rows.Next() {
		var e Entry
		var embBlob []byte
		if err := rows.Scan(&e.ID, &e.Category, &e.Content, &e.Source, &embBlob, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(embBlob) == 0 {
			continue
		}
		emb := DeserializeEmbedding(embBlob)
		score := CosineSimilarity(queryEmb, emb)
		results = append(results, scored{entry: e, score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	entries := make([]Entry, 0, limit)
	for i := 0; i < len(results) && i < limit; i++ {
		// Only include if similarity is above threshold.
		if results[i].score > 0.3 {
			entries = append(entries, results[i].entry)
		}
	}
	return entries, nil
}

func (s *Store) substringSearch(ctx context.Context, query string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, category, content, source, created_at
		FROM memories
		WHERE content LIKE ?
		ORDER BY created_at DESC
		LIMIT 20
	`, "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Category, &e.Content, &e.Source, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Recent returns the N most recent memories.
func (s *Store) Recent(ctx context.Context, limit int) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, category, content, source, created_at
		FROM memories
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Category, &e.Content, &e.Source, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Delete removes a memory by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
	return err
}

// FormatForContext formats memories for inclusion in the agent's context.
func FormatForContext(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<learned_memories>\n")

	// Group by category.
	categories := make(map[string][]Entry)
	for _, e := range entries {
		categories[e.Category] = append(categories[e.Category], e)
	}

	for category, memories := range categories {
		sb.WriteString(fmt.Sprintf("  <category name=%q>\n", category))
		for _, m := range memories {
			sb.WriteString(fmt.Sprintf("    - %s\n", m.Content))
		}
		sb.WriteString("  </category>\n")
	}

	sb.WriteString("</learned_memories>")
	return sb.String()
}

func generateID() string {
	return fmt.Sprintf("mem_%d", time.Now().UnixNano())
}
