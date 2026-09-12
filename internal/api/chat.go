package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/openpak/website/internal/chat"
	"github.com/openpak/website/internal/store"
)

// The chat surface of the mobile API (universal-social-prd US-5): the same
// messages the website shows, readable and sendable from the phone. A message
// sent here is announced to the recipient's consoles by the chat service.
//
//	GET  /api/v1/me/chat                      conversations, newest message first
//	GET  /api/v1/me/chat/{peer}?after=<id>    history with one peer, oldest first
//	POST /api/v1/me/chat/{peer}               {"body"} -> 201, the stored message
func (s *Server) registerChat(mux *http.ServeMux) {
	g := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("GET /api/v1"+pattern, h) }
	h := func(pattern string, hh http.HandlerFunc) { mux.HandleFunc("POST /api/v1"+pattern, hh) }
	g("/me/chat", s.auth(s.handleChatConversations))
	g("/me/chat/{peer}", s.auth(s.handleChatHistory))
	h("/me/chat/{peer}", s.auth(s.handleChatSend))
}

func chatErr(w http.ResponseWriter, err error) {
	if errors.Is(err, chat.ErrUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "chat is not available on this deployment")
		return
	}
	writeError(w, http.StatusBadGateway, "chat service error")
}

func (s *Server) handleChatConversations(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	convs, err := s.Chat.Conversations(r.Context(), tok.AccountID)
	if err != nil {
		chatErr(w, err)
		return
	}
	if convs == nil {
		convs = []chat.Conversation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": convs})
}

func (s *Server) handleChatHistory(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	after := int64(0)
	if v := r.URL.Query().Get("after"); v != "" {
		var perr error
		after, perr = strconv.ParseInt(v, 10, 64)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "after must be a message id")
			return
		}
	}
	ms, err := s.Chat.History(r.Context(), tok.AccountID, r.PathValue("peer"), after)
	if err != nil {
		chatErr(w, err)
		return
	}
	if ms == nil {
		ms = []chat.Message{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": ms})
}

func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var in struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(in.Body) == "" {
		writeError(w, http.StatusBadRequest, "body required")
		return
	}
	m, err := s.Chat.Send(r.Context(), tok.AccountID, r.PathValue("peer"), in.Body, "")
	if err != nil {
		chatErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": m})
}
