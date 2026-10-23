package erasure

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/store"
)

type fakeCore struct {
	due       []string
	finalized []string
	failFinal bool
}

func (f *fakeCore) DueDeletions(_ context.Context, limit int) ([]string, error) {
	if len(f.due) > limit {
		return append([]string(nil), f.due[:limit]...), nil
	}
	return append([]string(nil), f.due...), nil
}

func (f *fakeCore) FinalizeDeletion(_ context.Context, id string) error {
	if f.failFinal {
		return errors.New("core down")
	}
	f.finalized = append(f.finalized, id)
	for i, d := range f.due {
		if d == id {
			f.due = append(f.due[:i], f.due[i+1:]...)
			break
		}
	}
	return nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Every step runs for a due account, and the core finalizes only once all of
// them succeeded; a failing step leaves the account due, and the next run,
// with the service back, finishes it.
func TestFinaliserFinalizesOnlyWhenEveryStepSucceeds(t *testing.T) {
	ctx := context.Background()
	fc := &fakeCore{due: []string{"a1", "a2"}}
	chatDown := true
	var calls []string
	f := &Finaliser{Core: fc, Lock: store.NewMemory(), Log: quiet(), Steps: []Step{
		{Name: "saves", Erase: func(_ context.Context, id string) error { calls = append(calls, "saves:"+id); return nil }},
		{Name: "chat", Erase: func(_ context.Context, id string) error {
			calls = append(calls, "chat:"+id)
			if chatDown && id == "a2" {
				return errors.New("chat unreachable")
			}
			return nil
		}},
	}}
	res, err := f.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Due != 2 || res.Erased != 1 || res.Failed != 1 || strings.Join(fc.finalized, ",") != "a1" {
		t.Fatalf("first run: %+v finalized=%v", res, fc.finalized)
	}
	if strings.Join(calls, ",") != "saves:a1,chat:a1,saves:a2,chat:a2" {
		t.Fatalf("steps ran %v", calls)
	}
	chatDown = false
	res, err = f.RunOnce(ctx)
	if err != nil || res.Erased != 1 || strings.Join(fc.finalized, ",") != "a1,a2" {
		t.Fatalf("retry: %+v %v finalized=%v", res, err, fc.finalized)
	}
	if res, _ := f.RunOnce(ctx); res.Due != 0 {
		t.Fatalf("nothing should be due: %+v", res)
	}
}

// A core that cannot finalize leaves the account due for the next run.
func TestFinaliserRetriesCoreFinalize(t *testing.T) {
	fc := &fakeCore{due: []string{"a1"}, failFinal: true}
	f := &Finaliser{Core: fc, Lock: store.NewMemory(), Log: quiet()}
	if res, _ := f.RunOnce(context.Background()); res.Erased != 0 || res.Failed != 1 || len(fc.due) != 1 {
		t.Fatalf("%+v due=%v", res, fc.due)
	}
	fc.failFinal = false
	if res, _ := f.RunOnce(context.Background()); res.Erased != 1 {
		t.Fatalf("retry: %+v", res)
	}
}

// Two instances: the one without the lease does nothing.
func TestFinaliserLease(t *testing.T) {
	db := store.NewMemory()
	unlock, ok, err := db.TryLock(context.Background(), lockName)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	fc := &fakeCore{due: []string{"a1"}}
	f := &Finaliser{Core: fc, Lock: db, Log: quiet()}
	if res, err := f.RunOnce(context.Background()); err != nil || res.Due != 0 || len(fc.finalized) != 0 {
		t.Fatalf("ran without the lease: %+v %v", res, err)
	}
	unlock()
	if res, _ := f.RunOnce(context.Background()); res.Erased != 1 {
		t.Fatalf("after unlock: %+v", res)
	}
}

// More due accounts than one page: every one of them is reached in one run.
func TestFinaliserPages(t *testing.T) {
	fc := &fakeCore{}
	for i := 0; i < pageSize+7; i++ {
		fc.due = append(fc.due, "acct-"+time.Duration(i).String())
	}
	f := &Finaliser{Core: fc, Lock: store.NewMemory(), Log: quiet()}
	if res, _ := f.RunOnce(context.Background()); res.Erased != pageSize+7 || len(fc.due) != 0 {
		t.Fatalf("%+v left=%d", res, len(fc.due))
	}
}

// The website step takes ticket and crash files, avatar files and every row
// of the account, and only of the account.
func TestWebsiteStep(t *testing.T) {
	ctx := context.Background()
	db := store.NewMemory()
	dir := t.TempDir()
	files, err := attachments.New(filepath.Join(dir, "att"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := media.NewFSStorage(filepath.Join(dir, "media"))
	if err != nil {
		t.Fatal(err)
	}
	avatars := media.New(st)
	const gone, stays = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	for _, id := range []string{gone, stays} {
		if err := avatars.StorePreset(ctx, id, media.PresetForAccount(id)); err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertMedia(ctx, store.MediaRecord{AccountID: id, Kind: "avatar", UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := db.SavePushToken(ctx, id, "tok-"+id, "fcm"); err != nil {
			t.Fatal(err)
		}
	}
	att, err := files.Save("tickets", strings.NewReader("log"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTicket(ctx, store.Ticket{AccountID: gone, Subject: "help", Category: "other"},
		store.TicketMessage{AuthorID: gone, Body: "it broke", Attachment: store.Attachment{Key: att.Key, Name: "log.txt"}}); err != nil {
		t.Fatal(err)
	}
	if err := WebsiteStep(db, files, avatars, quiet()).Erase(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Open(att.Key); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("ticket attachment survives: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "media", "avatars", gone)); !os.IsNotExist(err) {
		t.Fatalf("avatar files survive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "media", "avatars", stays)); err != nil {
		t.Fatalf("neighbour's avatar went: %v", err)
	}
	if ts, _ := db.ListTickets(ctx, store.TicketFilter{AccountID: gone}, 10); len(ts) != 0 {
		t.Fatalf("tickets survive: %v", ts)
	}
	if _, err := db.GetMedia(ctx, gone, "avatar"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("media row survives: %v", err)
	}
	if toks, _ := db.PushTokensForAccount(ctx, stays); len(toks) != 1 {
		t.Fatalf("neighbour's push token went: %v", toks)
	}
	// Idempotent.
	if err := WebsiteStep(db, files, avatars, quiet()).Erase(ctx, gone); err != nil {
		t.Fatalf("repeat: %v", err)
	}
}
