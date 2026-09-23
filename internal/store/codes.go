package store

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// OpenPak codes: one per account, the code people swap to become friends
// whatever console they are on. Only the website and the app (through the
// website's API) take one, so the website owns them; the core and the console
// adapters never see them.
//
// Shape: OP-XXXX-XXXX from an alphabet with no 0/O, 1/I/L or U, so a code
// read out loud or copied by hand survives. 30^8 is about 6.6e11 codes, and a
// clash on mint is retried rather than prevented.
func init() {
	migrations["0012_openpak_codes"] = `CREATE TABLE IF NOT EXISTS openpak_codes (
		account_id text PRIMARY KEY,
		code text NOT NULL UNIQUE,
		created_at timestamptz NOT NULL DEFAULT now()
	);`
}

const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

// OpenPakCodeStore is the part of Store that mints and resolves OpenPak codes.
type OpenPakCodeStore interface {
	// OpenPakCode returns the account's code, minting one the first time.
	OpenPakCode(ctx context.Context, accountID string) (string, error)
	// AccountByOpenPakCode resolves a typed code (any case, dashes and the
	// OP prefix optional) to its account, or ErrNotFound.
	AccountByOpenPakCode(ctx context.Context, typed string) (string, error)
}

func newOpenPakCode() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	out := []byte("OP-XXXX-XXXX")
	for i, j := range []int{3, 4, 5, 6, 8, 9, 10, 11} {
		// ponytail: modulo bias of 256%30 is irrelevant for an identifier nobody guesses at scale.
		out[j] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(out)
}

// NormalizeOpenPakCode turns what someone typed into the stored form, or ""
// when it cannot be one.
func NormalizeOpenPakCode(typed string) string {
	s := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(typed))
	s = strings.TrimPrefix(s, "OP")
	if len(s) != 8 || strings.Trim(s, codeAlphabet) != "" {
		return ""
	}
	return "OP-" + s[:4] + "-" + s[4:]
}

func (s *PG) OpenPakCode(ctx context.Context, accountID string) (string, error) {
	for range 5 {
		var code string
		err := s.pool.QueryRow(ctx, `SELECT code FROM openpak_codes WHERE account_id=$1`, accountID).Scan(&code)
		if err == nil {
			return code, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		// ON CONFLICT (account_id) covers two tabs minting at once; a clash on
		// the code itself is a unique violation, and the loop draws again.
		_, err = s.pool.Exec(ctx, `INSERT INTO openpak_codes(account_id, code) VALUES ($1, $2)
			ON CONFLICT (account_id) DO NOTHING`, accountID, newOpenPakCode())
		var pgErr *pgconn.PgError
		if err != nil && !(errors.As(err, &pgErr) && pgErr.Code == "23505") {
			return "", err
		}
	}
	return "", errors.New("could not mint an OpenPak code")
}

func (s *PG) AccountByOpenPakCode(ctx context.Context, typed string) (string, error) {
	code := NormalizeOpenPakCode(typed)
	if code == "" {
		return "", ErrNotFound
	}
	var id string
	err := s.pool.QueryRow(ctx, `SELECT account_id FROM openpak_codes WHERE code=$1`, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (m *Memory) OpenPakCode(_ context.Context, accountID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.codes == nil {
		m.codes = map[string]string{}
	}
	if c, ok := m.codes[accountID]; ok {
		return c, nil
	}
	c := newOpenPakCode()
	m.codes[accountID] = c
	return c, nil
}

func (m *Memory) AccountByOpenPakCode(_ context.Context, typed string) (string, error) {
	code := NormalizeOpenPakCode(typed)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, c := range m.codes {
		if code != "" && c == code {
			return id, nil
		}
	}
	return "", ErrNotFound
}
