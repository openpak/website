package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/voice"
)

// Voice chat for the phone app (package voice says what a room is and how
// the signaling goes):
//
//	GET  /api/v1/me/voice               the room of the game you are in right now, or null
//	POST /api/v1/me/voice/rooms         a new code room
//	GET  /api/v1/me/voice/rooms/{code}  a room and who is talking in it
//	GET  /api/v1/me/voice/ws?code=      the signaling socket (WebSocket upgrade)
//
// and, for the console's lobby page (served by nx-baas, which knows the
// console's account and asks here):
//
//	GET  /internal/voice/room?account_id=&app_id=   Bearer WEBSITE_INTERNAL_KEY
func (s *Server) registerVoice(mux *http.ServeMux) {
	h := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("POST /api/v1"+pattern, h) }
	g := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("GET /api/v1"+pattern, h) }
	g("/me/voice", s.auth(s.handleVoiceCurrent))
	h("/me/voice/rooms", s.auth(s.handleVoiceCreate))
	g("/me/voice/rooms/{code}", s.auth(s.handleVoiceRoom))
	g("/me/voice/ws", bearerFromSubprotocol(s.auth(s.handleVoiceSocket)))
	mux.HandleFunc("GET /internal/voice/room", s.handleInternalVoiceRoom)
}

// newVoice reads the deployment's voice settings. The hub always exists:
// code rooms need nothing but this process. Without VOICE_GATHERING_URLS
// there is no game room to find; without the TURN settings two phones behind
// strict NATs will not hear each other.
func newVoice() (*voice.Hub, *voice.Gatherings) {
	return voice.New(voice.Config{
			TURNSecret: os.Getenv("VOICE_TURN_SECRET"),
			TURNURLs:   splitList(os.Getenv("VOICE_TURN_URLS")),
			STUNURLs:   splitList(os.Getenv("VOICE_STUN_URLS")),
		}),
		voice.NewGatherings(splitList(os.Getenv("VOICE_GATHERING_URLS")), os.Getenv("NX_INTERNAL_KEY"))
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// voiceMember is someone in a game room: in the game, and maybe talking.
type voiceMember struct {
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
	InVoice     bool   `json:"in_voice"`
}

type voiceRoom struct {
	voice.Room
	MaxMembers int           `json:"max_members"`
	Members    []voiceMember `json:"members"`
}

// gameRoom is the account's game room, or nil when it is in no gathering a
// title server knows of (or none could be asked).
func (s *Server) gameRoom(ctx context.Context, accountID string) (*voiceRoom, *voice.Gathering) {
	g, err := s.Gatherings.Of(ctx, accountID)
	if err != nil {
		s.log.Warn("voice: a title server did not answer the gathering lookup", "err", err)
	}
	if g == nil {
		return nil, nil
	}
	return s.describeRoom(s.Voice.GameRoom(g.Title, g.GID), g), g
}

// describeRoom lists a room's members: a game room's are the gathering's
// players with an account, plus anyone who came in by the code; a code
// room's are whoever is connected.
func (s *Server) describeRoom(room voice.Room, g *voice.Gathering) *voiceRoom {
	_, peers, _ := s.Voice.Lookup(room.Code)
	out := &voiceRoom{Room: room, MaxMembers: voice.MaxPeers, Members: []voiceMember{}}
	talking := map[string]bool{}
	for _, p := range peers {
		talking[p.ID] = true
	}
	seen := map[string]bool{}
	if g != nil {
		for _, p := range g.Participants {
			if p.AccountID == "" || seen[p.AccountID] {
				continue
			}
			seen[p.AccountID] = true
			out.Members = append(out.Members, voiceMember{AccountID: p.AccountID, DisplayName: p.Nickname, InVoice: talking[p.AccountID]})
		}
	}
	for _, p := range peers {
		if !seen[p.ID] {
			out.Members = append(out.Members, voiceMember{AccountID: p.ID, DisplayName: p.Name, InVoice: true})
		}
	}
	return out
}

func (s *Server) handleVoiceCurrent(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	room, _ := s.gameRoom(r.Context(), tok.AccountID)
	writeJSON(w, http.StatusOK, map[string]any{"room": room, "max_members": voice.MaxPeers})
}

func (s *Server) handleVoiceCreate(w http.ResponseWriter, r *http.Request, _ store.APIToken) {
	writeJSON(w, http.StatusCreated, map[string]any{"room": s.describeRoom(s.Voice.CreateRoom(), nil)})
}

func (s *Server) handleVoiceRoom(w http.ResponseWriter, r *http.Request, _ store.APIToken) {
	room, _, ok := s.Voice.Lookup(r.PathValue("code"))
	if !ok {
		writeError(w, http.StatusNotFound, "no voice room has that code")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room": s.describeRoom(room, nil)})
}

// bearerFromSubprotocol lets a browser, which cannot set headers on a
// WebSocket, present its bearer as the subprotocol "bearer.<token>". The app
// sends the Authorization header like every other call.
func bearerFromSubprotocol(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			for _, p := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
				if t, ok := strings.CutPrefix(strings.TrimSpace(p), "bearer."); ok && t != "" {
					r.Header.Set("Authorization", "Bearer "+t)
				}
			}
		}
		next(w, r)
	}
}

func (s *Server) handleVoiceSocket(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	code := r.URL.Query().Get("code")
	room, peers, ok := s.Voice.Lookup(code)
	if !ok {
		writeError(w, http.StatusNotFound, "no voice room has that code")
		return
	}
	already := false
	for _, p := range peers {
		already = already || p.ID == tok.AccountID
	}
	if len(peers) >= voice.MaxPeers && !already {
		writeError(w, http.StatusConflict, "the voice room is full")
		return
	}
	name := tok.AccountID
	if acct, err := s.Identity.GetAccount(r.Context(), tok.AccountID); err == nil && acct.DisplayName != "" {
		name = acct.DisplayName
	}
	// The bearer is the credential, not a cookie, so a page on another origin
	// gains nothing by opening this socket: the origin check is not needed.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       []string{"openpak-voice"},
		InsecureSkipVerify: true,
	})
	if err != nil {
		return // Accept has answered
	}
	defer ws.CloseNow()
	// The request context ends with the handler; the socket outlives neither.
	err = s.Voice.Join(r.Context(), room.Code, voice.Peer{ID: tok.AccountID, Name: name}, ws)
	if err != nil {
		code := "voice_room_unknown"
		if errors.Is(err, voice.ErrRoomFull) {
			code = "voice_room_full"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		b, _ := json.Marshal(map[string]string{"type": "error", "code": code, "message": err.Error()})
		_ = ws.Write(ctx, websocket.MessageText, b)
		cancel()
		ws.Close(websocket.StatusPolicyViolation, code)
		return
	}
	ws.Close(websocket.StatusNormalClosure, "")
}

// handleInternalVoiceRoom answers the console's lobby page (via nx-baas):
// the room of the game this account is in, and how many are talking.
func (s *Server) handleInternalVoiceRoom(w http.ResponseWriter, r *http.Request) {
	key := internalKey()
	if key == "" {
		writeError(w, http.StatusServiceUnavailable, "internal surface is not configured")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	account := r.URL.Query().Get("account_id")
	if account == "" {
		writeError(w, http.StatusBadRequest, "account_id is required")
		return
	}
	room, g := s.gameRoom(r.Context(), account)
	if room == nil {
		writeError(w, http.StatusNotFound, "not in a game")
		return
	}
	inVoice := 0
	for _, m := range room.Members {
		if m.InVoice {
			inVoice++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code": room.Code, "title": room.Title, "max_members": room.MaxMembers,
		"players": len(g.Participants), "in_voice": inVoice, "members": room.Members,
	})
}
