package push

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

type fakeEvents struct {
	events []core.Event
	max    uint64
}

func (f *fakeEvents) Poll(_ context.Context, since uint64, limit int32) ([]core.Event, uint64, error) {
	var out []core.Event
	for _, e := range f.events {
		if e.Version > since {
			out = append(out, e)
		}
	}
	return out, f.max, nil
}

type fakeIdentity struct{}

func (fakeIdentity) ResolveAccount(_ context.Context, _, _ string) (core.Account, error) {
	return core.Account{}, nil
}
func (fakeIdentity) GetAccount(_ context.Context, id string) (core.Account, error) {
	return core.Account{ID: id, DisplayName: "Leia"}, nil
}

type fakeInvites struct {
	pending        []core.PendingInvitation
	players        []core.TitlePlayers
	networkPlayers map[string]int
	totalPlayers   int
}

func (f *fakeInvites) ListPending(_ context.Context, _ string) ([]core.PendingInvitation, error) {
	return f.pending, nil
}
func (f *fakeInvites) Decline(_ context.Context, _, _ string) (bool, error) { return true, nil }
func (f *fakeInvites) PlayerCounts(_ context.Context) ([]core.TitlePlayers, map[string]int, int, error) {
	return f.players, f.networkPlayers, f.totalPlayers, nil
}
func (f *fakeInvites) Presence(_ context.Context, _ []string) (map[string]core.Presence, error) {
	return nil, nil
}

type sent struct{ token, title, body string }

type fakeSender struct {
	sent []sent
	gone map[string]bool
}

func (f *fakeSender) Send(_ context.Context, token, title, body string, _ map[string]string) error {
	if f.gone[token] {
		return ErrGone
	}
	f.sent = append(f.sent, sent{token, title, body})
	return nil
}

func newNotifier(t *testing.T, ev *fakeEvents, inv *fakeInvites, s Sender) (*Notifier, store.Store) {
	t.Helper()
	db := store.NewMemory()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewNotifier(ev, fakeIdentity{}, inv, db, s, log), db
}

// The first run of a deployment must not treat the whole event history as
// unread: every friend request ever made would arrive at once on someone's
// lock screen.
func TestFirstRunDoesNotReplayHistory(t *testing.T) {
	ev := &fakeEvents{max: 42, events: []core.Event{
		{Version: 7, Type: "friend_requested", AccountID: "a", SubjectID: "b"},
	}}
	s := &fakeSender{}
	n, db := newNotifier(t, ev, &fakeInvites{}, s)
	if err := db.SavePushToken(context.Background(), "a", "tok-a", "fcm"); err != nil {
		t.Fatal(err)
	}
	if err := n.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := n.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.sent) != 0 {
		t.Fatalf("history was replayed as notifications: %+v", s.sent)
	}
	if v, _ := db.PushCursor(context.Background()); v != 42 {
		t.Fatalf("cursor = %d, want the present (42)", v)
	}
}

func TestNotifiesOnFriendRequestAndInvitation(t *testing.T) {
	ctx := context.Background()
	ev := &fakeEvents{}
	inv := &fakeInvites{pending: []core.PendingInvitation{
		{ID: "inv-9", FromName: "Han", TitleID: "0100E65002BB8000", ExpiresAt: time.Now().Add(time.Minute)},
	}}
	s := &fakeSender{}
	n, db := newNotifier(t, ev, inv, s)
	_ = db.SavePushToken(ctx, "a", "tok-a", "fcm")
	_ = db.SetPushCursor(ctx, 100) // already caught up; only new events count

	ev.events = []core.Event{
		{Version: 101, Type: "friend_requested", AccountID: "a", SubjectID: "b"},
		{Version: 102, Type: "invitation_sent", AccountID: "a", SubjectID: "inv-9"},
		{Version: 103, Type: "account_banned", AccountID: "a"}, // not notifiable
	}
	ev.max = 103
	if err := n.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.sent) != 2 {
		t.Fatalf("want two notifications, got %+v", s.sent)
	}
	if s.sent[0].title != "Friend request" || s.sent[0].body != "Leia wants to be friends" {
		t.Errorf("friend request text: %+v", s.sent[0])
	}
	if s.sent[1].title != "Invitation" || s.sent[1].body != "Han invited you to play" {
		t.Errorf("invitation text: %+v", s.sent[1])
	}
	if v, _ := db.PushCursor(ctx); v != 103 {
		t.Errorf("cursor = %d, want 103", v)
	}
	// A second tick with nothing new sends nothing again.
	if err := n.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.sent) != 2 {
		t.Errorf("events were re-notified: %+v", s.sent)
	}
}

// A token the transport says is dead is forgotten, or every future
// notification retries an uninstalled app forever.
func TestDeadTokenIsForgotten(t *testing.T) {
	ctx := context.Background()
	ev := &fakeEvents{max: 5, events: []core.Event{
		{Version: 5, Type: "friend_requested", AccountID: "a", SubjectID: "b"},
	}}
	s := &fakeSender{gone: map[string]bool{"dead": true}}
	n, db := newNotifier(t, ev, &fakeInvites{}, s)
	_ = db.SavePushToken(ctx, "a", "dead", "fcm")
	_ = db.SavePushToken(ctx, "a", "live", "fcm")
	_ = db.SetPushCursor(ctx, 4)

	if err := n.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.sent) != 1 || s.sent[0].token != "live" {
		t.Fatalf("the live install should still be notified: %+v", s.sent)
	}
	toks, _ := db.PushTokensForAccount(ctx, "a")
	if len(toks) != 1 || toks[0].Token != "live" {
		t.Fatalf("dead token was kept: %+v", toks)
	}
}

// One failing send must not stall the cursor, or a single bad token freezes
// notifications for everyone.
func TestOneFailureDoesNotStallTheCursor(t *testing.T) {
	ctx := context.Background()
	ev := &fakeEvents{max: 9, events: []core.Event{
		{Version: 9, Type: "friend_requested", AccountID: "a", SubjectID: "b"},
	}}
	n, db := newNotifier(t, ev, &fakeInvites{}, senderFunc(func() error {
		return errors.New("transport is down")
	}))
	_ = db.SavePushToken(ctx, "a", "tok", "fcm")
	_ = db.SetPushCursor(ctx, 8)
	if err := n.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := db.PushCursor(ctx); v != 9 {
		t.Errorf("cursor = %d, want it to advance past a failed send", v)
	}
}

type senderFunc func() error

func (f senderFunc) Send(context.Context, string, string, string, map[string]string) error {
	return f()
}
