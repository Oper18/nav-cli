package db

import "fmt"

// Chunk is one row of the chunks manifest: the embedded state of a single
// Qdrant point. ChunkID is always qdrant.ID(branch, symbol, chunkNumber) —
// the manifest piggybacks on the ID scheme Qdrant already uses to
// disambiguate points, it never invents a second one.
type Chunk struct {
	ChunkID      string
	File         string
	Symbol       string
	ContentHash  string
	EmbeddedHash string
	UpdatedAt    int64
}

// UpsertChunk writes or replaces a manifest row.
func UpsertChunk(exec Execer, c Chunk) error {
	_, err := exec.Exec(`
		INSERT INTO chunks (chunk_id, file, symbol, content_hash, embedded_hash, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(chunk_id) DO UPDATE SET
			file = excluded.file,
			symbol = excluded.symbol,
			content_hash = excluded.content_hash,
			embedded_hash = excluded.embedded_hash,
			updated_at = excluded.updated_at
	`, c.ChunkID, c.File, c.Symbol, c.ContentHash, c.EmbeddedHash, c.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upserting chunk %q: %w", c.ChunkID, err)
	}
	return nil
}

// ChunksForFile returns every manifest row recorded for file.
func ChunksForFile(exec Execer, file string) ([]Chunk, error) {
	rows, err := exec.Query(`
		SELECT chunk_id, file, symbol, content_hash, embedded_hash, updated_at
		FROM chunks WHERE file = ?
	`, file)
	if err != nil {
		return nil, fmt.Errorf("querying chunks for %q: %w", file, err)
	}
	defer rows.Close()
	return scanChunks(rows)
}

// DeleteChunk removes a single manifest row by its chunk_id.
func DeleteChunk(exec Execer, chunkID string) error {
	if _, err := exec.Exec(`DELETE FROM chunks WHERE chunk_id = ?`, chunkID); err != nil {
		return fmt.Errorf("deleting chunk %q: %w", chunkID, err)
	}
	return nil
}

// DeleteChunksForFile removes every manifest row for a deleted file.
func DeleteChunksForFile(exec Execer, file string) error {
	if _, err := exec.Exec(`DELETE FROM chunks WHERE file = ?`, file); err != nil {
		return fmt.Errorf("deleting chunks for %q: %w", file, err)
	}
	return nil
}

// AllFiles returns the distinct set of files present in the manifest, used by
// the non-git mtime fallback to know what has already been synced at least
// once.
func AllFiles(exec Execer) ([]string, error) {
	rows, err := exec.Query(`SELECT DISTINCT file FROM chunks`)
	if err != nil {
		return nil, fmt.Errorf("listing manifest files: %w", err)
	}
	defer rows.Close()

	var files []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, fmt.Errorf("scanning file: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func scanChunks(rows interface {
	Next() bool
	Scan(...interface{}) error
	Err() error
}) ([]Chunk, error) {
	var out []Chunk
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ChunkID, &c.File, &c.Symbol, &c.ContentHash, &c.EmbeddedHash, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning chunk row: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
