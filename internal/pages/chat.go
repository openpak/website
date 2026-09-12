package pages

import (
	"errors"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/openpak/website/internal/chat"
	"github.com/openpak/website/internal/i18n"
)

// The chat page (universal-social-prd US-5, platform-wiiu-prd WU-4): the
// same message store the phone reads and the Wii U surfaces read. A message
// sent here is the one that appears on a friend's console. Server-rendered,
// no script: a conversation is a form post away.

type chatView struct {
	baseData
	Available bool
	Peers     []chatPeer        // friends who could join a conversation
	Threads   []chatThread      // existing conversations, newest message first
	With      *chatPeer         // the open conversation, when ?with= names a friend
	Messages  []chatLine        // its history, oldest last
	ReplyTo   string            // pre-fills the reply box after ?with=
	Names     map[string]string // peer account id -> display name
}

type chatPeer struct {
	AccountID   string
	DisplayName string
}

type chatThread struct {
	Peer     chatPeer
	Newest   chat.Message
	When     string
	Outgoing bool   // the newest line is the viewer's own
	Preview  string // the newest line as it may be shown: plain text, or a kind name
}

type chatLine struct {
	Body     string
	When     string
	Outgoing bool
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	ctx := r.Context()
	view := chatView{baseData: s.data(r, v, i18n.T("en", "chat.title")), Names: map[string]string{}}
	if s.Chat == nil {
		s.render(w, http.StatusOK, "chat.html", view)
		return
	}
	view.Available = true

	friends, err := s.Social.ListFriends(ctx, v.AccountID)
	if err != nil {
		s.log.Error("chat: friends", "err", err)
	}
	for _, f := range friends {
		view.Names[f.AccountID] = f.DisplayName
		view.Peers = append(view.Peers, chatPeer{AccountID: f.AccountID, DisplayName: f.DisplayName})
	}
	nameOf := func(id string) string {
		if n, ok := view.Names[id]; ok && n != "" {
			return n
		}
		return id
	}

	convs, err := s.Chat.Conversations(ctx, v.AccountID)
	if err != nil {
		if !errors.Is(err, chat.ErrUnavailable) {
			s.log.Error("chat: conversations", "err", err)
		}
	}
	for _, c := range convs {
		view.Threads = append(view.Threads, chatThread{
			Peer:     chatPeer{AccountID: c.Peer, DisplayName: nameOf(c.Peer)},
			Newest:   c.Newest,
			When:     c.Newest.SentAt().UTC().Format("2006-01-02 15:04"),
			Outgoing: c.Newest.From == v.AccountID,
			Preview:  lineBody(c.Newest),
		})
	}

	// An open conversation: only a friend can be on the other side, because
	// only a friend could have messages with us in the first place.
	with := r.URL.Query().Get("with")
	if with != "" {
		for _, p := range view.Peers {
			if p.AccountID == with {
				view.With = &p
				break
			}
		}
	}
	if view.With != nil {
		ms, err := s.Chat.History(ctx, v.AccountID, with, 0)
		if err != nil {
			s.log.Error("chat: history", "err", err)
		}
		for _, m := range ms {
			view.Messages = append(view.Messages, chatLine{
				Body:     lineBody(m),
				When:     m.SentAt().UTC().Format("2006-01-02 15:04"),
				Outgoing: m.From == v.AccountID,
			})
		}
	}
	s.render(w, http.StatusOK, "chat.html", view)
}

// lineBody renders one message for the page: plain text as itself, the other
// kinds named, escaped either way. The store's bytes never reach the browser
// as markup.
func lineBody(m chat.Message) string {
	if m.Kind == "" || m.Kind == "text" {
		return html.EscapeString(m.Body)
	}
	return html.EscapeString("[" + strings.ToUpper(m.Kind[:1]) + m.Kind[1:] + " message]")
}

func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	peer := r.PostFormValue("peer")
	body := strings.TrimSpace(r.PostFormValue("body"))
	back := "/account/chat"
	if peer != "" {
		back += "?with=" + url.QueryEscape(peer)
	}
	if s.Chat == nil {
		http.Redirect(w, r, back+"&m=chat+unavailable", http.StatusSeeOther)
		return
	}
	if body == "" || peer == "" {
		http.Redirect(w, r, back+"&m=Write+a+message+first", http.StatusSeeOther)
		return
	}
	if _, err := s.Chat.Send(r.Context(), v.AccountID, peer, body, ""); err != nil {
		s.log.Error("chat send", "err", err)
		http.Redirect(w, r, back+"&m=The+message+could+not+be+sent", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
