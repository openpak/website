package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The shipped ids are well formed, each names one title only, and the migration builds.
func TestCatalogTitleIDsData(t *testing.T) {
	seen := map[string]string{}
	for _, e := range catalogTitleIDs {
		if len(e.IDs) == 0 {
			t.Errorf("%s: no ids", e.Name)
		}
		for _, id := range e.IDs {
			if !titleIDPattern.MatchString(id) {
				t.Errorf("%s: malformed %q", e.Name, id)
			}
			if prev, ok := seen[id]; ok {
				t.Errorf("%s shared by %s and %s", id, prev, e.Name)
			}
			seen[id] = e.Name
			switch e.Console {
			case "Wii U":
				if !strings.HasPrefix(id, "00050000") && !strings.HasPrefix(id, "00050010") && !strings.HasPrefix(id, "00040000") {
					t.Errorf("%s: %s is not a Wii U game", e.Name, id)
				}
			case "3DS":
				if !strings.HasPrefix(id, "00040000") {
					t.Errorf("%s: %s is not a 3DS game", e.Name, id)
				}
			case "Switch":
				if !strings.HasPrefix(id, "01") || !strings.HasSuffix(id, "000") {
					t.Errorf("%s: %s is not a Switch base program", e.Name, id)
				}
			default:
				t.Errorf("%s: console %q", e.Name, e.Console)
			}
		}
	}
	if sql := migrations["0008_catalog_title_ids"]; !strings.Contains(sql, `'Devil''s Third'`) {
		t.Error("migration does not quote names")
	}
}

// title_ids is the list, title_id its first; a bare title_id replaces only the primary, ""
// clears them, and an id held by another title is refused.
func TestRequestTitleIDs(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	a, err := m.CreateRequest(ctx, Request{Kind: "title", Name: "Mario Kart 7", Console: "3DS", Status: "beta",
		TitleIDs: []string{"0004000000030800", "0004000000030700", "0004000000030800"}}, "t")
	if err != nil || a.TitleID != "0004000000030800" || len(a.TitleIDs) != 2 {
		t.Fatalf("create: %+v %v", a, err)
	}
	b, _ := m.CreateRequest(ctx, Request{Kind: "title", Name: "Other", Console: "3DS", Status: "beta"}, "t")
	if _, err := m.UpdateRequest(ctx, b.ID, RequestPatch{TitleIDs: &[]string{"0004000000030700"}}, "t"); !errors.Is(err, ErrDuplicate) {
		t.Errorf("an id another title holds: %v", err)
	}
	if _, err := m.UpdateRequest(ctx, b.ID, RequestPatch{TitleIDs: &[]string{"nope"}}, "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a malformed id: %v", err)
	}
	primary := "0004000000030600"
	a, err = m.UpdateRequest(ctx, a.ID, RequestPatch{TitleID: &primary}, "t")
	if err != nil || strings.Join(a.TitleIDs, ",") != "0004000000030600,0004000000030700" {
		t.Fatalf("replace primary: %+v %v", a.TitleIDs, err)
	}
	ch := m.RequestChanges(a.ID)
	if got := ch[len(ch)-1].Changes["title_ids"]; got.New != "0004000000030600,0004000000030700" {
		t.Errorf("title_ids change not logged: %+v", got)
	}
	empty := ""
	if a, err = m.UpdateRequest(ctx, a.ID, RequestPatch{TitleID: &empty}, "t"); err != nil || a.TitleID != "" || len(a.TitleIDs) != 0 {
		t.Errorf("clear: %+v %v", a, err)
	}
}
