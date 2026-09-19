package push

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

// Sender is what actually reaches a phone. It exists so the notifier can be
// tested without a network and a service account, not to invite a plugin
// architecture: FCM is the only transport today.
type Sender interface {
	Send(ctx context.Context, token, title, body string, data map[string]string) error
}

// Notifier turns core events into notifications. It is the only thing in the
// stack that pushes: apps never talk to the core, and the core knows nothing
// about phones.
type Notifier struct {
	Events   core.Events
	Identity core.Identity
	Invites  core.InvitationsView
	DB       store.Store
	Sender   Sender
	Interval time.Duration
	log      *slog.Logger
}

func NewNotifier(ev core.Events, id core.Identity, inv core.InvitationsView, db store.Store, s Sender, log *slog.Logger) *Notifier {
	return &Notifier{Events: ev, Identity: id, Invites: inv, DB: db, Sender: s,
		Interval: 5 * time.Second, log: log}
}

// Run polls until the context is cancelled. The cursor lives in the database,
// so a restart resumes where it stopped rather than replaying or skipping.
func (n *Notifier) Run(ctx context.Context) {
	if err := n.start(ctx); err != nil {
		n.log.Error("push: cannot start", "err", err)
		return
	}
	t := time.NewTicker(n.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := n.tick(ctx); err != nil {
				n.log.Error("push: tick", "err", err)
			}
		}
	}
}

// start refuses to treat the whole of history as unread. A fresh install has
// cursor 0, and every past friend request would arrive as a notification at
// once; the first run therefore skips to the present.
func (n *Notifier) start(ctx context.Context) error {
	cursor, err := n.DB.PushCursor(ctx)
	if err != nil {
		return err
	}
	if cursor > 0 {
		return nil
	}
	// Not Poll(0, 1): its max_version is the end of that one-event page, so a fresh
	// install would start at version 1 and replay everything after it.
	head, err := core.EventHead(ctx, n.Events, 0)
	if err != nil {
		return err
	}
	n.log.Info("push: starting from the present", "version", head)
	return n.DB.SetPushCursor(ctx, head)
}

func (n *Notifier) tick(ctx context.Context) error {
	cursor, err := n.DB.PushCursor(ctx)
	if err != nil {
		return err
	}
	events, max, err := n.Events.Poll(ctx, cursor, 100)
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := n.notify(ctx, e); err != nil {
			// One undeliverable notification must not stall the cursor and
			// with it every later one: log and carry on.
			n.log.Error("push: notify", "err", err, "type", e.Type, "account", e.AccountID)
		}
	}
	if max > cursor {
		return n.DB.SetPushCursor(ctx, max)
	}
	return nil
}

func (n *Notifier) notify(ctx context.Context, e core.Event) error {
	title, body, data := n.compose(ctx, e)
	if title == "" {
		return nil // not a notifiable event
	}
	tokens, err := n.DB.PushTokensForAccount(ctx, e.AccountID)
	if err != nil {
		return err
	}
	for _, t := range tokens {
		err := n.Sender.Send(ctx, t.Token, title, body, data)
		switch {
		case err == nil:
		case errors.Is(err, ErrGone):
			// The install is gone; forget it rather than failing on it forever.
			if err := n.DB.DeletePushToken(ctx, t.Token); err != nil {
				n.log.Error("push: forget token", "err", err)
			}
		default:
			n.log.Error("push: send", "err", err)
		}
	}
	return nil
}

// compose turns an event into what a person reads on a lock screen. The data
// map is what the app uses to open the right screen when it is tapped.
func (n *Notifier) compose(ctx context.Context, e core.Event) (title, body string, data map[string]string) {
	switch e.Type {
	case "friend_requested":
		who := "Someone"
		if acct, err := n.Identity.GetAccount(ctx, e.SubjectID); err == nil && acct.DisplayName != "" {
			who = acct.DisplayName
		}
		return "Friend request", who + " wants to be friends",
			map[string]string{"type": "friend_request", "account_id": e.SubjectID}

	case "friend_accepted":
		who := "Someone"
		if acct, err := n.Identity.GetAccount(ctx, e.SubjectID); err == nil && acct.DisplayName != "" {
			who = acct.DisplayName
		}
		return "Friend request accepted", who + " is now your friend",
			map[string]string{"type": "friend_accepted", "account_id": e.SubjectID}

	case "invitation_sent":
		// The event names the invitation, not its sender; the inbox knows who
		// it is from, and reading it consumes nothing.
		from, title := "Someone", ""
		if ins, err := n.Invites.ListPending(ctx, e.AccountID); err == nil {
			for _, in := range ins {
				if in.ID == e.SubjectID {
					if in.FromName != "" {
						from = in.FromName
					}
					title = in.TitleID
					break
				}
			}
		}
		return "Invitation", from + " invited you to play",
			map[string]string{"type": "invitation", "invitation_id": e.SubjectID, "title_id": title}
	}
	return "", "", nil
}
