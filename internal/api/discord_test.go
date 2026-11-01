package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/openpak/website/internal/store"
)

type fakeNotice struct {
	ok  bool
	err error
}

func (f fakeNotice) NoticeAccepted(context.Context, string) (bool, error) { return f.ok, f.err }

func TestInternalDiscordLookup(t *testing.T) {
	s, fc, db := newTestAPI(t)
	ctx := context.Background()
	_ = db.LinkDiscord(ctx, store.DiscordLink{AccountID: "acct-adult", DiscordID: "111111111111111111", Username: "adult"})
	_ = db.LinkDiscord(ctx, store.DiscordLink{AccountID: "acct-bad", DiscordID: "222222222222222222"})
	fc.status = map[string]string{"acct-bad": "banned", "acct-gone": "missing"}
	_ = db.LinkDiscord(ctx, store.DiscordLink{AccountID: "acct-gone", DiscordID: "333333333333333333"})
	get := func(id, auth string) (int, map[string]any) {
		rec := doRaw(t, s, http.MethodGet, "/internal/discord/"+id, auth, nil)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}

	if c, _ := get("111111111111111111", "Bearer x"); c != http.StatusServiceUnavailable {
		t.Fatalf("no key configured: %d", c)
	}
	t.Setenv("WEBSITE_INTERNAL_KEY", "k")
	if c, _ := get("111111111111111111", "Bearer wrong"); c != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", c)
	}
	if c, _ := get("", "Bearer k"); c == http.StatusOK {
		t.Fatalf("empty id answered 200")
	}
	if c, _ := get("abc", "Bearer k"); c != http.StatusBadRequest {
		t.Fatalf("bad id: %d", c)
	}

	// Unknown Discord id.
	if c, m := get("999999999999999999", "Bearer k"); c != 200 || m["linked"] != false {
		t.Fatalf("unlinked: %d %v", c, m)
	}
	// Adult, notice source absent: null.
	fc.profile[1] = "1990-01-01"
	c, m := get("111111111111111111", "Bearer k")
	if c != 200 || m["linked"] != true || m["account_id"] != "acct-adult" || m["supervised_or_minor"] != false {
		t.Fatalf("adult: %d %v", c, m)
	}
	if v, ok := m["pakka_notice_accepted"]; !ok || v != nil {
		t.Fatalf("notice should be null: %v", m)
	}
	// Notice source set.
	s.PakkaNotice = fakeNotice{ok: true}
	if _, m = get("111111111111111111", "Bearer k"); m["pakka_notice_accepted"] != true {
		t.Fatalf("accepted: %v", m)
	}
	s.PakkaNotice = fakeNotice{err: errors.New("down")}
	if _, m = get("111111111111111111", "Bearer k"); m["pakka_notice_accepted"] != nil {
		t.Fatalf("source error must be null: %v", m)
	}
	// Minor, unknown birthday: both children.
	for _, b := range []string{"2015-05-05", ""} {
		fc.profile[1] = b
		if _, m = get("111111111111111111", "Bearer k"); m["supervised_or_minor"] != true {
			t.Fatalf("birthday %q must be a child: %v", b, m)
		}
	}
	// Banned and vanished accounts are not linked as far as Pakka goes.
	for _, id := range []string{"222222222222222222", "333333333333333333"} {
		if c, m := get(id, "Bearer k"); c != 200 || m["linked"] != false {
			t.Fatalf("%s: %d %v", id, c, m)
		}
	}
	// After unlink the id is unlinked again.
	_, _ = db.UnlinkDiscord(ctx, "acct-adult")
	if _, m = get("111111111111111111", "Bearer k"); m["linked"] != false {
		t.Fatalf("after unlink: %v", m)
	}
}
