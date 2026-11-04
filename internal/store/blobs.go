package store

import "github.com/jackc/pgx/v5/pgxpool"

// Avatars and ticket/crash attachments live in Postgres, not on a volume.
func init() {
	migrations["0026_blobs"] = `CREATE TABLE IF NOT EXISTS media_objects (
		key text PRIMARY KEY,
		data bytea NOT NULL,
		updated_at timestamptz NOT NULL DEFAULT now()
	);

	CREATE TABLE IF NOT EXISTS attachment_files (
		key text PRIMARY KEY,
		data bytea NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now()
	)`
}

// Pool is the connection pool, for the blob stores (media, attachments).
func (s *PG) Pool() *pgxpool.Pool { return s.pool }
