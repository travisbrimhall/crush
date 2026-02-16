// Package memory provides persistent cross-session memory for the agent.
// This file implements associative memory with graph-based retrieval.
package memory

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MemoryStore defines the interface for memory storage operations.
// Both Store and AssociativeStore implement this.
type MemoryStore interface {
	Save(ctx context.Context, entry Entry) error
	Search(ctx context.Context, query string) ([]Entry, error)
	List(ctx context.Context, category string) ([]Entry, error)
	Recent(ctx context.Context, limit int) ([]Entry, error)
	Delete(ctx context.Context, id string) error
}

// AssociativeMemoryStore extends MemoryStore with graph traversal.
type AssociativeMemoryStore interface {
	MemoryStore
	SaveWithLinks(ctx context.Context, entry Entry) error
	Associate(ctx context.Context, query string, hops int) ([]Entry, error)
}

// Link represents a weighted edge between two memories.
type Link struct {
	TargetID string
	Weight   float32 // Semantic similarity at link creation
	CoOccur  int     // Times accessed together (reinforcement)
}

// AssociativeEntry extends Entry with graph edges.
type AssociativeEntry struct {
	Entry
	Links       []Link
	AccessCount int       // Total times retrieved
	LastAccess  time.Time // For decay calculations
}

// AssociativeStore wraps Store with graph traversal capabilities.
type AssociativeStore struct {
	*Store
	linkThreshold   float32 // Minimum similarity to create a link
	maxLinksPerNode int     // Cap on outbound edges
}

// NewAssociativeStore creates a store with associative retrieval.
func NewAssociativeStore(db *sql.DB, ollamaURL, model string) (*AssociativeStore, error) {
	base, err := NewStoreWithEmbeddings(db, ollamaURL, model)
	if err != nil {
		return nil, err
	}

	s := &AssociativeStore{
		Store:           base,
		linkThreshold:   0.4, // Only link memories with >0.4 similarity
		maxLinksPerNode: 10,
	}

	if err := s.migrateAssociative(); err != nil {
		return nil, fmt.Errorf("failed to migrate associative tables: %w", err)
	}

	return s, nil
}

func (s *AssociativeStore) migrateAssociative() error {
	_, err := s.db.Exec(`
		-- Links between memories (graph edges)
		CREATE TABLE IF NOT EXISTS memory_links (
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			weight REAL NOT NULL DEFAULT 0,
			co_occur INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (source_id, target_id),
			FOREIGN KEY (source_id) REFERENCES memories(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES memories(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_links_source ON memory_links(source_id);
		CREATE INDEX IF NOT EXISTS idx_links_target ON memory_links(target_id);
		CREATE INDEX IF NOT EXISTS idx_links_weight ON memory_links(weight);

		-- Track access patterns for decay/reinforcement
		ALTER TABLE memories ADD COLUMN access_count INTEGER DEFAULT 0;
		ALTER TABLE memories ADD COLUMN last_access DATETIME;
	`)
	// Ignore "duplicate column" errors from ALTER TABLE on re-runs
	if err != nil && !isColumnExistsError(err) {
		return err
	}
	return nil
}

func isColumnExistsError(err error) bool {
	// SQLite returns "duplicate column name" for existing columns
	return err != nil && (err.Error() == "duplicate column name: access_count" ||
		err.Error() == "duplicate column name: last_access")
}

// SaveWithLinks stores a memory and creates links to similar existing memories.
func (s *AssociativeStore) SaveWithLinks(ctx context.Context, entry Entry) error {
	// First, save the entry normally (which generates embedding).
	if err := s.Store.Save(ctx, entry); err != nil {
		return err
	}

	// If no vector support, skip link creation.
	if !s.useVector || s.embedder == nil {
		return nil
	}

	// Find similar existing memories to link to.
	neighbors, err := s.Store.semanticSearch(ctx, entry.Content, s.maxLinksPerNode*2)
	if err != nil {
		return nil // Non-fatal: memory saved, just no links
	}

	// Create bidirectional links for sufficiently similar memories.
	for _, neighbor := range neighbors {
		if neighbor.ID == entry.ID {
			continue
		}

		// Compute similarity (we need embeddings for both).
		similarity := s.computeSimilarity(ctx, entry.Content, neighbor.Content)
		if similarity < s.linkThreshold {
			continue
		}

		// Create bidirectional links.
		if err := s.createLink(ctx, entry.ID, neighbor.ID, similarity); err != nil {
			continue // Non-fatal
		}
		if err := s.createLink(ctx, neighbor.ID, entry.ID, similarity); err != nil {
			continue // Non-fatal
		}
	}

	return nil
}

func (s *AssociativeStore) computeSimilarity(ctx context.Context, content1, content2 string) float32 {
	emb1, err := s.embedder.Embed(ctx, content1)
	if err != nil {
		return 0
	}
	emb2, err := s.embedder.Embed(ctx, content2)
	if err != nil {
		return 0
	}
	return CosineSimilarity(emb1, emb2)
}

func (s *AssociativeStore) createLink(ctx context.Context, sourceID, targetID string, weight float32) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_links (source_id, target_id, weight)
		VALUES (?, ?, ?)
		ON CONFLICT(source_id, target_id) DO UPDATE SET
			weight = MAX(memory_links.weight, excluded.weight),
			updated_at = CURRENT_TIMESTAMP
	`, sourceID, targetID, weight)
	return err
}

// Associate retrieves memories by graph traversal from semantic entry points.
func (s *AssociativeStore) Associate(ctx context.Context, query string, hops int) ([]Entry, error) {
	if hops <= 0 {
		hops = 2 // Default: entry points + 2 levels of links
	}

	// Find entry points via semantic search.
	seeds, err := s.Store.semanticSearch(ctx, query, 3)
	if err != nil {
		// Fallback to substring if semantic fails.
		seeds, err = s.Store.substringSearch(ctx, query)
		if err != nil {
			return nil, err
		}
	}

	if len(seeds) == 0 {
		return nil, nil
	}

	// BFS traversal following links.
	seen := make(map[string]bool)
	results := make([]Entry, 0)
	accessedIDs := make([]string, 0)

	queue := seeds
	for depth := 0; depth <= hops && len(queue) > 0; depth++ {
		var next []Entry
		for _, entry := range queue {
			if seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			results = append(results, entry)
			accessedIDs = append(accessedIDs, entry.ID)

			// Follow links to neighbors.
			neighbors, err := s.getLinkedMemories(ctx, entry.ID)
			if err != nil {
				continue
			}
			next = append(next, neighbors...)
		}
		queue = next
	}

	// Record access for reinforcement (async-safe to ignore errors).
	go s.recordAccess(context.Background(), accessedIDs)

	return results, nil
}

func (s *AssociativeStore) getLinkedMemories(ctx context.Context, sourceID string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.category, m.content, m.source, m.created_at
		FROM memories m
		JOIN memory_links l ON m.id = l.target_id
		WHERE l.source_id = ?
		  AND (l.weight > 0.5 OR l.co_occur >= 2)
		ORDER BY (l.weight + l.co_occur * 0.1) DESC
		LIMIT 5
	`, sourceID)
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

// recordAccess updates access counts and timestamps for retrieved memories.
func (s *AssociativeStore) recordAccess(ctx context.Context, ids []string) {
	if len(ids) == 0 {
		return
	}

	// Update access count and timestamp.
	for _, id := range ids {
		s.db.ExecContext(ctx, `
			UPDATE memories 
			SET access_count = access_count + 1, last_access = CURRENT_TIMESTAMP
			WHERE id = ?
		`, id)
	}

	// Increment co-occurrence for all pairs accessed together.
	// This strengthens links between memories retrieved in the same query.
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			s.db.ExecContext(ctx, `
				UPDATE memory_links 
				SET co_occur = co_occur + 1, updated_at = CURRENT_TIMESTAMP
				WHERE (source_id = ? AND target_id = ?) OR (source_id = ? AND target_id = ?)
			`, ids[i], ids[j], ids[j], ids[i])
		}
	}
}

// DecayLinks weakens links that haven't been reinforced recently.
// Call periodically (e.g., daily) to allow forgetting.
func (s *AssociativeStore) DecayLinks(ctx context.Context, olderThan time.Duration, decayFactor float32) error {
	cutoff := time.Now().Add(-olderThan)
	_, err := s.db.ExecContext(ctx, `
		UPDATE memory_links
		SET weight = weight * ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE updated_at < ? AND co_occur = 0
	`, decayFactor, cutoff)
	return err
}

// PruneWeakLinks removes links that have decayed below threshold.
func (s *AssociativeStore) PruneWeakLinks(ctx context.Context, minWeight float32) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM memory_links WHERE weight < ? AND co_occur < 2
	`, minWeight)
	return err
}

// GetCluster returns all memories connected to a given memory within N hops.
// Useful for visualizing memory structure or debugging.
func (s *AssociativeStore) GetCluster(ctx context.Context, memoryID string, hops int) ([]Entry, error) {
	// Retrieve the seed memory's content to use as query.
	var content string
	err := s.db.QueryRowContext(ctx, `SELECT content FROM memories WHERE id = ?`, memoryID).Scan(&content)
	if err != nil {
		return nil, err
	}

	// Use Associate but start from this specific memory.
	seed, err := s.get(ctx, memoryID)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	results := []Entry{seed}
	seen[memoryID] = true

	queue := []Entry{seed}
	for depth := 0; depth < hops && len(queue) > 0; depth++ {
		var next []Entry
		for _, entry := range queue {
			neighbors, err := s.getLinkedMemories(ctx, entry.ID)
			if err != nil {
				continue
			}
			for _, n := range neighbors {
				if !seen[n.ID] {
					seen[n.ID] = true
					results = append(results, n)
					next = append(next, n)
				}
			}
		}
		queue = next
	}

	return results, nil
}

func (s *AssociativeStore) get(ctx context.Context, id string) (Entry, error) {
	var e Entry
	err := s.db.QueryRowContext(ctx, `
		SELECT id, category, content, source, created_at
		FROM memories WHERE id = ?
	`, id).Scan(&e.ID, &e.Category, &e.Content, &e.Source, &e.CreatedAt)
	return e, err
}
