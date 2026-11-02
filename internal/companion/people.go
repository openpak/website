package companion

import (
	"context"
	"sync"
	"time"
)

// People turns the ids a game server ranks (a network player id or an OpenPak account id) into
// the names and pictures the site shows. Game servers never send names: the account core owns
// them. A player nobody has linked is shown as "Player".
type People struct {
	// AccountByPID maps a player id in a network (the account-core link namespace) to an account.
	AccountByPID func(ctx context.Context, network, pid string) (string, error)
	// PIDByAccount is the reverse: the id the game server stores for an account, "" for none.
	PIDByAccount func(ctx context.Context, network, accountID string) (string, error)
	// Name is the account's display name.
	Name func(ctx context.Context, accountID string) (string, error)
	// Avatar is the site-relative picture URL of an account, "" for the default.
	Avatar func(ctx context.Context, accountID string) string

	mu    sync.Mutex
	cache map[string]cachedPerson
}

// Person is a resolved player.
type Person struct {
	AccountID, Name, Avatar string
}

type cachedPerson struct {
	p   Person
	exp time.Time
}

const peopleTTL = 2 * time.Minute

// Resolve names the player of a ranking row. Either id may be empty.
func (pp *People) Resolve(ctx context.Context, network, accountID, pid string) Person {
	if pp == nil {
		return Person{}
	}
	key := network + "|" + accountID + "|" + pid
	pp.mu.Lock()
	if c, ok := pp.cache[key]; ok && time.Now().Before(c.exp) {
		pp.mu.Unlock()
		return c.p
	}
	pp.mu.Unlock()
	if accountID == "" && pid != "" && pp.AccountByPID != nil {
		accountID, _ = pp.AccountByPID(ctx, network, pid)
	}
	var p Person
	if accountID != "" {
		p.AccountID = accountID
		if pp.Name != nil {
			p.Name, _ = pp.Name(ctx, accountID)
		}
		if pp.Avatar != nil {
			p.Avatar = pp.Avatar(ctx, accountID)
		}
	}
	pp.mu.Lock()
	if pp.cache == nil || len(pp.cache) > 20000 {
		pp.cache = map[string]cachedPerson{}
	}
	pp.cache[key] = cachedPerson{p, time.Now().Add(peopleTTL)}
	pp.mu.Unlock()
	return p
}

// Subject is the id a game server of this network stores for an account: the player id, or
// the account id itself when the network has no link.
func (pp *People) Subject(ctx context.Context, network, accountID string) string {
	if pp != nil && pp.PIDByAccount != nil && accountID != "" {
		if pid, err := pp.PIDByAccount(ctx, network, accountID); err == nil && pid != "" {
			return pid
		}
	}
	return accountID
}
