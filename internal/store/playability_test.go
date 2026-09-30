package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// playabilityOf is the one title's rows as a map, for the assertions below.
func playabilityOf(t *testing.T, m *Memory, id int64) map[string]string {
	t.Helper()
	rows, err := m.ListPlayability(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, p := range rows {
		if p.RequestID == id {
			out[p.Client] = p.Status
		}
	}
	return out
}

// A playability patch merges: only the clients it names move, "" deletes one, a client it
// leaves out keeps what it had, and the lower-case name a client sends is the grid's spelling.
// Each real change is logged and moves last_updated; a patch that changes nothing does neither.
func TestPlayabilityMerge(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	r, err := m.CreateRequest(ctx, Request{Kind: "title", Name: "Outbound", Console: "Switch", Status: "live"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		patch map[string]string
		want  map[string]string
		noop  bool
	}{
		{name: "two clients at once",
			patch: map[string]string{"Ryujinx": "playable", "Citron": "ingame"},
			want:  map[string]string{"Ryujinx": "playable", "Citron": "ingame"}},
		{name: "a third leaves the first two alone",
			patch: map[string]string{"Eden": "menus"},
			want:  map[string]string{"Ryujinx": "playable", "Citron": "ingame", "Eden": "menus"}},
		{name: "an empty status deletes only that client",
			patch: map[string]string{"Citron": ""},
			want:  map[string]string{"Ryujinx": "playable", "Eden": "menus"}},
		{name: "the header's lower-case name is the grid's client",
			patch: map[string]string{"ryujinx": "boots"},
			want:  map[string]string{"Ryujinx": "boots", "Eden": "menus"}},
		{name: "deleting what was never there changes nothing",
			patch: map[string]string{"Cemu": ""},
			want:  map[string]string{"Ryujinx": "boots", "Eden": "menus"}, noop: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := m.UpdateRequest(ctx, r.ID, RequestPatch{}, "t")
			logged := len(m.RequestChanges(r.ID))
			after, err := m.UpdateRequest(ctx, r.ID, RequestPatch{Playability: tc.patch}, "admin")
			if err != nil {
				t.Fatalf("patch: %v", err)
			}
			got := playabilityOf(t, m, r.ID)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for client, status := range tc.want {
				if got[client] != status {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
			if tc.noop {
				if len(m.RequestChanges(r.ID)) != logged || after.LastUpdated.After(before.LastUpdated) {
					t.Fatal("a patch that changes nothing must not log or move last_updated")
				}
				return
			}
			if !after.LastUpdated.After(before.LastUpdated) {
				t.Error("last_updated did not move")
			}
			ch := m.RequestChanges(r.ID)
			last := ch[len(ch)-1]
			if len(ch) != logged+1 || last.Actor != "admin" || last.Action != "update" {
				t.Fatalf("change log: %+v", last)
			}
			for client := range tc.patch {
				key := "playability." + playabilityClients[strings.ToLower(client)]
				if _, ok := last.Changes[key]; !ok {
					t.Errorf("no %s in the change log: %+v", key, last.Changes)
				}
			}
		})
	}
}

// Only the five words all three emulators localize are accepted, and only clients the /titles
// grid already names. Anything else is ErrInvalid and writes nothing at all.
func TestPlayabilityValidation(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	r, _ := m.CreateRequest(ctx, Request{Kind: "title", Name: "Outbound", Console: "Switch", Status: "live"}, "t")
	for _, ok := range PlayabilityStates {
		if _, err := m.UpdateRequest(ctx, r.ID, RequestPatch{Playability: map[string]string{"Ryujinx": ok}}, "t"); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	if _, err := m.UpdateRequest(ctx, r.ID, RequestPatch{Playability: map[string]string{"Ryujinx": " playable "}}, "t"); err != nil {
		t.Errorf("a padded value is trimmed, not refused: %v", err)
	}
	for _, bad := range []map[string]string{
		{"Ryujinx": "Playable"},         // the five words are lower case
		{"Ryujinx": "perfect"},          // another compatibility list's vocabulary
		{"Ryujinx": "in-game"},          // a synonym none of the three localizes
		{"Ryujinx": "intro"},            // not one of the five
		{"Rujinx": "playable"},          // a typo must not invent a client
		{"Everything": "menus"},         // not a client on the grid
		{"": "playable"},                // no client at all
		{"Eden": "menus", "X": "boots"}, // one bad entry refuses the whole patch
	} {
		if _, err := m.UpdateRequest(ctx, r.ID, RequestPatch{Playability: bad}, "t"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: %v, want ErrInvalid", bad, err)
		}
	}
	if got := playabilityOf(t, m, r.ID); len(got) != 1 || got["Ryujinx"] != "playable" {
		t.Fatalf("a refused patch wrote something: %v", got)
	}
}
