package store

func init() {
	migrations["0012_openpak_codes"] = `CREATE TABLE IF NOT EXISTS openpak_codes (
		account_id text PRIMARY KEY,
		code text NOT NULL UNIQUE,
		created_at timestamptz NOT NULL DEFAULT now()
	);`
}

func init() { migrations["0022_retire_openpak_codes"] = `DROP TABLE IF EXISTS openpak_codes;` }
