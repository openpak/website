// Package support is the help-ticket logic shared by the website pages, the
// app's /api/v1/me/tickets and the admin queue: open, reply, note, and who
// may see what. The website has no mail path of its own (the account core
// sends only its account mails), so a staff reply reaches the person in the
// app and on the site, plus a push notification when FCM is configured.
package support

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"

	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/push"
	"github.com/openpak/website/internal/store"
)

// MaxAttachment is the largest file a ticket message may carry.
const MaxAttachment = 10 << 20

// ErrNoAttachments is a file sent to a deployment with no attachment storage.
var ErrNoAttachments = errors.New("support: attachments are not configured")

// Upload is an optional file with a message.
type Upload struct {
	Name string
	Body io.Reader
}

// Service holds what the ticket flows need.
type Service struct {
	DB    store.Store
	Files *attachments.Store // nil: messages carry no files
	Push  push.Sender        // nil: no notification on a staff reply
	Log   *slog.Logger
}

func (s *Service) save(up *Upload) (store.Attachment, error) {
	if up == nil {
		return store.Attachment{}, nil
	}
	if s.Files == nil {
		return store.Attachment{}, ErrNoAttachments
	}
	saved, err := s.Files.Save("tickets", up.Body, MaxAttachment)
	if err != nil {
		return store.Attachment{}, err
	}
	return store.Attachment{Key: saved.Key, Name: attachments.CleanName(up.Name), ContentType: saved.ContentType, Size: saved.Size}, nil
}

// Open creates a ticket with its first message.
func (s *Service) Open(ctx context.Context, t store.Ticket, body string, up *Upload) (store.Ticket, error) {
	if err := store.ValidateTicket(t.Subject, t.Category, body); err != nil {
		return t, err
	}
	att, err := s.save(up)
	if err != nil {
		return t, err
	}
	out, err := s.DB.CreateTicket(ctx, t, store.TicketMessage{Body: body, Attachment: att})
	if err != nil && att.Key != "" {
		s.Files.Remove(att.Key)
	}
	return out, err
}

// Own returns a ticket and its public thread if accountID opened it; anyone
// else gets store.ErrNotFound, so ticket ids reveal nothing.
func (s *Service) Own(ctx context.Context, id int64, accountID string) (store.Ticket, []store.TicketMessage, error) {
	t, err := s.DB.GetTicket(ctx, id)
	if err != nil || t.AccountID != accountID {
		return store.Ticket{}, nil, store.ErrNotFound
	}
	msgs, err := s.DB.TicketMessages(ctx, id, false)
	return t, msgs, err
}

// Reply adds the person's own message; it puts the ticket back in the queue
// (open), closed or not.
func (s *Service) Reply(ctx context.Context, id int64, accountID, body string, up *Upload) (store.TicketMessage, error) {
	if _, _, err := s.Own(ctx, id, accountID); err != nil {
		return store.TicketMessage{}, err
	}
	return s.add(ctx, store.TicketMessage{TicketID: id, AuthorID: accountID, Body: body}, up, store.TicketOpen)
}

// StaffReply is an operator's message. internal notes change nothing the
// person can see; a public reply waits on the person unless status says
// otherwise (closed, to reply and close at once).
func (s *Service) StaffReply(ctx context.Context, id int64, actor, body string, internal bool, up *Upload, status string) (store.TicketMessage, error) {
	if status == "" && !internal {
		status = store.TicketWaiting
	}
	m, err := s.add(ctx, store.TicketMessage{TicketID: id, AuthorID: actor, Staff: true, Internal: internal, Body: body}, up, status)
	if err == nil && !internal {
		s.notify(ctx, id)
	}
	return m, err
}

func (s *Service) add(ctx context.Context, m store.TicketMessage, up *Upload, status string) (store.TicketMessage, error) {
	if err := store.ValidateTicketBody(m.Body); err != nil {
		return m, err
	}
	att, err := s.save(up)
	if err != nil {
		return m, err
	}
	m.Attachment = att
	out, err := s.DB.AddTicketMessage(ctx, m, status)
	if err != nil && att.Key != "" {
		s.Files.Remove(att.Key)
	}
	return out, err
}

// notify tells the person's phones that staff answered. Best effort: the
// reply is saved whatever happens here.
func (s *Service) notify(ctx context.Context, id int64) {
	if s.Push == nil {
		return
	}
	t, err := s.DB.GetTicket(ctx, id)
	if err != nil {
		return
	}
	toks, err := s.DB.PushTokensForAccount(ctx, t.AccountID)
	if err != nil {
		return
	}
	for _, tok := range toks {
		if err := s.Push.Send(ctx, tok.Token, "New reply to your help request", t.Subject,
			map[string]string{"type": "ticket_reply", "ticket_id": strconv.FormatInt(id, 10)}); err != nil && s.Log != nil {
			s.Log.Info("support: push", "err", err)
		}
	}
}

// Attachment finds the file on one message the viewer may read: their own
// ticket's public messages, or any message for staff.
func (s *Service) Attachment(ctx context.Context, messageID int64, accountID string, staff bool) (store.Attachment, error) {
	m, err := s.DB.GetTicketMessage(ctx, messageID)
	if err != nil || m.Attachment.Key == "" {
		return store.Attachment{}, store.ErrNotFound
	}
	if !staff {
		t, err := s.DB.GetTicket(ctx, m.TicketID)
		if err != nil || t.AccountID != accountID || m.Internal {
			return store.Attachment{}, store.ErrNotFound
		}
	}
	return m.Attachment, nil
}
