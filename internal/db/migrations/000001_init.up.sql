CREATE TABLE IF NOT EXISTS chunks (
	chunk_id      TEXT PRIMARY KEY,
	file          TEXT NOT NULL,
	symbol        TEXT NOT NULL,
	content_hash  TEXT NOT NULL,
	embedded_hash TEXT NOT NULL,
	updated_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file);

CREATE TABLE IF NOT EXISTS nodes (
	id      TEXT PRIMARY KEY,
	kind    TEXT NOT NULL,
	name    TEXT NOT NULL,
	file    TEXT,
	line    INTEGER,
	summary TEXT
);

CREATE TABLE IF NOT EXISTS edges (
	src  TEXT NOT NULL,
	dst  TEXT NOT NULL,
	kind TEXT NOT NULL,
	PRIMARY KEY (src, dst, kind)
);

CREATE INDEX IF NOT EXISTS idx_edges_src ON edges(src);
CREATE INDEX IF NOT EXISTS idx_edges_dst ON edges(dst);

CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
