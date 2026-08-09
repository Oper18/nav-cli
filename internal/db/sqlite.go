// This file holds the per-project SQLite state nav keeps under
// ~/.nav/projects/<project>/: a manifest of embedded chunk hashes (for lazy
// re-embedding) and a knowledge graph of packages/files/symbols and their
// relationships. It lives under the user's home directory, keyed by project
// name, rather than inside the repository being indexed — a repo can be
// indexed under more than one project name (or from more than one clone),
// and keeping nav's own state out of the working tree means there is never
// anything for it to accidentally leak into `git status` or a commit. The
// graph is not a project-wide fact — it reflects whatever code the current
// branch happens to have, and that can differ meaningfully from branch to
// branch — so each branch gets its own database file (nav-<branch>.db),
// keyed by branch name. It uses modernc.org/sqlite (pure Go, no cgo) and
// github.com/mattermost/morph to apply schema migrations idempotently.
package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattermost/morph"
	sqlitedriver "github.com/mattermost/morph/drivers/sqlite"
	"github.com/mattermost/morph/sources/embedded"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationNames lists the embedded migration files in order. morph's
// embedded source needs bare filenames (its version regex is anchored at the
// start of the string, so a "migrations/" prefix would fail to parse).
var migrationNames = []string{
	"000001_init.up.sql",
}

// DB wraps the per-project SQLite connection.
type DB struct {
	sql *sql.DB
}

// Dir returns the path to ~/.nav/projects/<project>, creating it if
// necessary. project is used as-is, the same convention as
// config.ProjectDir — nav project names are short registry slugs (see
// ResolveProject), not arbitrary user input, so no extra sanitizing is
// applied here.
func Dir(project string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".nav", "projects", project)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return dir, nil
}

// branchFileToken converts a branch name into a filesystem-safe token for
// use in a per-branch database filename. Git branch names may contain '/'
// (e.g. "feature/foo") and other characters unsafe in a single path
// segment; those are replaced with '_'. A short hash of the original name is
// appended so that two branches which happen to sanitize to the same token
// (e.g. "feature/foo" and "feature_foo") never collide on the same file.
func branchFileToken(branch string) string {
	if branch == "" {
		branch = "_detached"
	}
	sum := sha256.Sum256([]byte(branch))
	hash := hex.EncodeToString(sum[:])[:8]

	var b strings.Builder
	for _, r := range branch {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String() + "-" + hash
}

// DBPath returns the path to ~/.nav/projects/<project>/nav-<branch>.db.
func DBPath(project, branch string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".nav", "projects", project, "nav-"+branchFileToken(branch)+".db")
}

// Exists reports whether branch already has a database file for project —
// i.e. whether nav has synced/indexed it before. It's used to restrict
// parent-branch candidates to branches that actually have embeddings to
// inherit from.
func Exists(project, branch string) bool {
	_, err := os.Stat(DBPath(project, branch))
	return err == nil
}

// LockPath returns the path to ~/.nav/projects/<project>/lock. The lock is
// shared across every branch: it serialises concurrent sync invocations
// against the same project (e.g. an overlapping git hook and prompt hook),
// not concurrent syncs of the same branch's data.
func LockPath(project string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".nav", "projects", project, "lock")
}

// ResetBranch deletes branch's SQLite database and its WAL/SHM sidecar
// files, so a subsequent Open(project, branch) recreates it from scratch via
// migrations. It is a no-op when no database exists yet for branch. The
// caller must not hold an open *DB for (project, branch) when calling
// ResetBranch.
func ResetBranch(project, branch string) error {
	base := DBPath(project, branch)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := base + suffix
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", path, err)
		}
	}
	return nil
}

// ResetAll deletes every branch's SQLite database (and WAL/SHM sidecars)
// under project's ~/.nav/projects/<project> directory, so a subsequent index
// run starts every branch from a completely clean slate. It is a no-op when
// that directory does not exist yet. The caller must not hold any open *DB
// for project when calling ResetAll.
func ResetAll(project string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".nav", "projects", project)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "nav-") || !strings.Contains(name, ".db") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", name, err)
		}
	}
	return nil
}

// Open creates ~/.nav/projects/<project> if needed, opens (or creates)
// branch's nav-<branch>.db in WAL mode with a 5s busy timeout, and applies
// any pending schema migrations.
func Open(project, branch string) (*DB, error) {
	if _, err := Dir(project); err != nil {
		return nil, err
	}

	path := DBPath(project, branch)
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&_pragma=foreign_keys(1)",
		path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	// SQLite has no real use for a connection pool; a single writer connection
	// avoids "database is locked" errors racing with the busy_timeout pragma.
	sqlDB.SetMaxOpenConns(1)

	if err := migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}

	return &DB{sql: sqlDB}, nil
}

// quietLogger discards morph's progress output; migrations are an
// implementation detail of nav sync's fast path and should not print
// anything on the common "nothing pending" case, nor even on first run.
type quietLogger struct{}

func (quietLogger) Printf(string, ...interface{}) {}
func (quietLogger) Println(...interface{})        {}

// migrate applies every pending embedded migration to db using morph.
func migrate(db *sql.DB) error {
	driver, err := sqlitedriver.WithInstance(db)
	if err != nil {
		return fmt.Errorf("creating migration driver: %w", err)
	}

	src, err := embedded.WithInstance(embedded.Resource(migrationNames, func(name string) ([]byte, error) {
		return migrationFiles.ReadFile(filepath.Join("migrations", name))
	}))
	if err != nil {
		return fmt.Errorf("loading embedded migrations: %w", err)
	}

	engine, err := morph.New(context.Background(), driver, src, morph.WithLogger(quietLogger{}))
	if err != nil {
		return fmt.Errorf("initialising migration engine: %w", err)
	}
	defer engine.Close()

	if err := engine.ApplyAll(); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}

// Close releases the underlying connection.
func (db *DB) Close() error {
	return db.sql.Close()
}

// Execer is the subset of *sql.DB / *sql.Tx used by the manifest/graph
// helpers, so the same functions work whether called standalone or inside a
// transaction.
type Execer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// Exec, Query, and QueryRow make *DB itself satisfy Execer, so callers can
// pass a *DB directly to the manifest/graph helpers outside of an explicit
// transaction, and a *sql.Tx (which already satisfies Execer natively)
// inside one.
func (db *DB) Exec(query string, args ...interface{}) (sql.Result, error) {
	return db.sql.Exec(query, args...)
}

func (db *DB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return db.sql.Query(query, args...)
}

func (db *DB) QueryRow(query string, args ...interface{}) *sql.Row {
	return db.sql.QueryRow(query, args...)
}

// GetMeta returns the value stored under key, and false when absent.
func (db *DB) GetMeta(key string) (string, bool, error) {
	return getMeta(db.sql, key)
}

func getMeta(exec Execer, key string) (string, bool, error) {
	var value string
	err := exec.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading meta %q: %w", key, err)
	}
	return value, true, nil
}

// SetMeta upserts a key/value pair using the given executor, so it can
// participate in a caller's transaction.
func SetMeta(exec Execer, key, value string) error {
	_, err := exec.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("writing meta %q: %w", key, err)
	}
	return nil
}

// SetMeta upserts a key/value pair outside of any explicit transaction.
func (db *DB) SetMeta(key, value string) error {
	return SetMeta(db.sql, key, value)
}

// WithTx runs fn inside a transaction, committing on success and rolling back
// on error or panic.
func (db *DB) WithTx(fn func(tx *sql.Tx) error) (err error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()
	if err = fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
