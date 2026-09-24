package accounts

import (
	"context"
	"testing"

	"github.com/openpak/website/internal/core"
)

// searchOnly answers SearchAccounts the way the core does: substring matches.
type searchOnly struct {
	core.Moderation
	rows []core.ModeratedAccount
}

func (s searchOnly) SearchAccounts(context.Context, string, int) ([]core.ModeratedAccount, error) {
	return s.rows, nil
}

func TestAccountByEmailOnlyExactActive(t *testing.T) {
	mod := searchOnly{rows: []core.ModeratedAccount{
		{ID: "partial", Email: "bob@example.com.au", Status: "active"},
		{ID: "banned", Email: "eve@example.com", Status: "banned"},
		{ID: "bob", Email: "Bob@Example.com", Status: "active"},
	}}
	for typed, want := range map[string]string{
		" bob@example.COM ": "bob",
		"eve@example.com":   "",
		"bob":               "", // not an address: no search at all
		"nobody@x.test":     "",
	} {
		if got := AccountByEmail(context.Background(), mod, typed); got != want {
			t.Errorf("%q: got %q, want %q", typed, got, want)
		}
	}
	if got := AccountByEmail(context.Background(), nil, "bob@example.com"); got != "" {
		t.Errorf("nil moderation: got %q", got)
	}
}
