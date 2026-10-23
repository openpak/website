package store

// Account deletion must take avatar metadata and push installs with it, and
// only the account's own: a surviving push token would keep notifying the
// device, and a neighbour's rows must never move.

import (
	"context"
	"testing"
	"time"
)

func TestDeletionDropsMediaAndPushTokens(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	if err := m.UpsertMedia(ctx, MediaRecord{AccountID: "gone", Kind: "avatar", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertMedia(ctx, MediaRecord{AccountID: "stays", Kind: "avatar", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := m.SavePushToken(ctx, "gone", "tok-gone", "android"); err != nil {
		t.Fatal(err)
	}
	if err := m.SavePushToken(ctx, "stays", "tok-stays", "android"); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteMediaForAccount(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	if err := m.RevokePushTokensForAccount(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetMedia(ctx, "gone", "avatar"); err != ErrNotFound {
		t.Fatalf("media after delete: %v", err)
	}
	if toks, _ := m.PushTokensForAccount(ctx, "gone"); len(toks) != 0 {
		t.Fatalf("push tokens survive: %+v", toks)
	}
	if _, err := m.GetMedia(ctx, "stays", "avatar"); err != nil {
		t.Fatalf("neighbour media moved: %v", err)
	}
	if toks, _ := m.PushTokensForAccount(ctx, "stays"); len(toks) != 1 {
		t.Fatalf("neighbour push tokens moved: %+v", toks)
	}
}
