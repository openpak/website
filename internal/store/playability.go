package store

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Playability is how far a game itself runs in one client. It is a different axis from
// Request.Status, which says how far OpenPak serves that game's online play: a title can be
// "playable" in Ryujinx and still have no server here, and the other way round.
type Playability struct {
	RequestID int64
	Client    string // the grid's spelling: "Ryujinx", "Eden", "Citron", "Switch", …
	Status    string // one of PlayabilityStates
}

// PlayabilityStates are the only five words a row may carry, best first. Ryujinx, Eden and
// Citron all localize exactly these, so a synonym here would be a string none of them can show.
var PlayabilityStates = []string{"playable", "ingame", "menus", "boots", "nothing"}

// ponytail: this table repeats the client names internal/pages/titles.go already spells, so a
// new emulator is two edits; fold it into one list here when pages can import it without a cycle.
// playabilityClients spells the clients the /titles compatibility grid uses
// (internal/pages/titles.go: playTargets, clientIcons, playClient). Clients send the
// lower-case key in X-OpenPak-Client; anything not listed is refused, so a typo cannot
// invent a column in three emulators' UIs.
var playabilityClients = map[string]string{
	"switch": "Switch", "wii u": "Wii U", "wiiu": "Wii U", "wii": "Wii", "3ds": "3DS", "ds": "DS",
	"xbox": "Xbox", "xbox 360": "Xbox 360", "xbox one": "Xbox One", "ps2": "PS2", "ps3": "PS3",
	"psp": "PSP", "vita": "Vita",
	"ryujinx": "Ryujinx", "eden": "Eden", "citron": "Citron", "cemu": "Cemu", "azahar": "Azahar",
	"dolphin": "Dolphin", "melonds": "melonDS", "xemu": "xemu", "xenia": "Xenia",
	"pcsx2": "PCSX2", "rpcs3": "RPCS3", "ppsspp": "PPSSPP", "vita3k": "Vita3K",
}

// PlayabilityStore is the per-client playability ledger. Writes go through UpdateRequest
// (RequestPatch.Playability) so the audit row and last_updated come from one place.
type PlayabilityStore interface {
	// ListPlayability returns every title's rows in one query; the catalogue reads are
	// per-request, not per-title.
	ListPlayability(ctx context.Context) ([]Playability, error)
}

func init() {
	migrations["0015_title_playability"] = `CREATE TABLE IF NOT EXISTS title_playability (
		request_id bigint NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
		client text NOT NULL,
		status text NOT NULL,
		updated_at timestamptz NOT NULL DEFAULT now(),
		PRIMARY KEY (request_id, client)
	);`
}

// normalizePlayability canonicalizes a patch's client names and checks its statuses. An
// empty status means "delete this client's row" and is kept.
func normalizePlayability(p map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(p))
	for client, status := range p {
		canon, ok := playabilityClients[strings.ToLower(strings.TrimSpace(client))]
		if !ok {
			return nil, fmt.Errorf("%w: unknown playability client %q", ErrInvalid, client)
		}
		status = strings.TrimSpace(status)
		if status != "" && !slices.Contains(PlayabilityStates, status) {
			return nil, fmt.Errorf("%w: playability must be one of %s", ErrInvalid, strings.Join(PlayabilityStates, ", "))
		}
		out[canon] = status
	}
	return out, nil
}

// playabilityDiff is the merge: only the named clients move, "" deletes, and a client the
// patch does not name is untouched. The result doubles as the request_changes entry.
func playabilityDiff(old, patch map[string]string) map[string]FieldChange {
	ch := map[string]FieldChange{}
	for client, want := range patch {
		if old[client] != want {
			ch["playability."+client] = FieldChange{old[client], want}
		}
	}
	return ch
}

func (s *PG) ListPlayability(ctx context.Context) ([]Playability, error) {
	rows, err := s.pool.Query(ctx, `SELECT request_id, client, status FROM title_playability ORDER BY request_id, client`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Playability
	for rows.Next() {
		var p Playability
		if err := rows.Scan(&p.RequestID, &p.Client, &p.Status); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// applyPlayability merges the patch inside the caller's transaction and returns what changed.
// Nothing is written when nothing differs, so a no-op patch leaves last_updated alone.
func applyPlayability(ctx context.Context, tx pgx.Tx, id int64, patch map[string]string) (map[string]FieldChange, error) {
	if len(patch) == 0 {
		return nil, nil
	}
	patch, err := normalizePlayability(patch)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT client, status FROM title_playability WHERE request_id=$1`, id)
	if err != nil {
		return nil, err
	}
	old := map[string]string{}
	for rows.Next() {
		var client, status string
		if err := rows.Scan(&client, &status); err != nil {
			rows.Close()
			return nil, err
		}
		old[client] = status
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ch := playabilityDiff(old, patch)
	for key, c := range ch {
		client := strings.TrimPrefix(key, "playability.")
		if c.New == "" {
			_, err = tx.Exec(ctx, `DELETE FROM title_playability WHERE request_id=$1 AND client=$2`, id, client)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO title_playability(request_id, client, status) VALUES($1,$2,$3)
				ON CONFLICT (request_id, client) DO UPDATE SET status=EXCLUDED.status, updated_at=now()`, id, client, c.New)
		}
		if err != nil {
			return nil, err
		}
	}
	return ch, nil
}

func (m *Memory) ListPlayability(_ context.Context) ([]Playability, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Playability
	for id, byClient := range m.playability {
		for client, status := range byClient {
			out = append(out, Playability{id, client, status})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RequestID != out[j].RequestID {
			return out[i].RequestID < out[j].RequestID
		}
		return out[i].Client < out[j].Client
	})
	return out, nil
}

// applyPlayability is Memory's half of the merge; the caller holds the lock.
func (m *Memory) applyPlayability(id int64, patch map[string]string) (map[string]FieldChange, error) {
	if len(patch) == 0 {
		return nil, nil
	}
	patch, err := normalizePlayability(patch)
	if err != nil {
		return nil, err
	}
	ch := playabilityDiff(m.playability[id], patch)
	for key, c := range ch {
		client := strings.TrimPrefix(key, "playability.")
		if c.New == "" {
			delete(m.playability[id], client)
			continue
		}
		if m.playability == nil {
			m.playability = map[int64]map[string]string{}
		}
		if m.playability[id] == nil {
			m.playability[id] = map[string]string{}
		}
		m.playability[id][client] = c.New.(string)
	}
	return ch, nil
}
