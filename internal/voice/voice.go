// Package voice is OpenPak's voice chat: rooms, WebRTC signaling and TURN
// credentials for the phone app.
//
// Nintendo's voice chat never ran on the console. A Switch game opened the
// Lobby applet, the page said "open the app on your phone", and the phone
// carried the audio. OpenPak does the same with its own app, so the server's
// whole job is to say who is in which room and pass the WebRTC offers,
// answers and ICE candidates between them. The audio itself goes phone to
// phone (or through the TURN relay when two phones cannot reach each other);
// it never passes through this process.
//
// A room is one of two things:
//
//   - a game room: the matchmaking gathering the player is in right now, as
//     the title server that owns it reports it (see Gatherings). The app finds
//     it without being told anything.
//   - a code room: made on request and joined by its six-character code, so
//     friends can talk outside a game.
//
// Both have a code; holding the code is what lets you in. A game room's code
// is only shown to the people in that gathering (in the app, and on the TV by
// the lobby page), so it works as the invitation for a player whose console
// login could not be tied to an account.
//
// Signaling protocol (JSON text frames over one WebSocket per phone):
//
//	server -> client
//	  {"type":"welcome","self":id,"name":yours,"room":{...},"peers":[peer...],"ice_servers":[...]}
//	  {"type":"join","peer":peer}            someone arrived
//	  {"type":"leave","id":id}               someone left
//	  {"type":"signal","from":id,"data":{}}  an SDP or ICE candidate for you
//	  {"type":"state","id":id,"muted":b,"speaking":b}
//	  {"type":"ping"}                        keeps proxies from idling the socket out
//	  {"type":"error","code":c,"message":m}  then the socket closes
//	client -> server
//	  {"type":"signal","to":id,"data":{}}    relayed as-is to that peer only
//	  {"type":"state","muted":b,"speaking":b}
//
// Who offers: the newcomer, to every peer listed in its welcome. Existing
// peers only ever answer, so two phones never offer to each other at once.
//
// Mesh ceiling: every phone sends its own audio to every other phone, so a
// room of n costs each phone n-1 uplink Opus streams (~30-40 kbit/s each) and
// n-1 decoders. MaxPeers = 8 keeps that to seven streams, well inside a mobile
// uplink and a phone's CPU; past that the cost grows with the square of the
// room and a selective forwarding unit (one uplink per phone) is the upgrade.
// Splatoon 2's private battles are eight players, which is where the number
// comes from.
package voice

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// MaxPeers is the mesh ceiling (see the package comment).
const MaxPeers = 8

// maxFrame bounds one signaling message. An SDP offer for one audio track is
// a few KiB; the cap is there so a client cannot make the server buffer much.
const maxFrame = 64 << 10

// pingEvery is under the idle timeouts of the proxies between a phone and
// here (Cloudflare's is 100 s).
const pingEvery = 25 * time.Second

// idleRoomTTL is how long an empty room keeps its code.
const idleRoomTTL = 15 * time.Minute

var (
	ErrUnknownRoom = errors.New("no voice room has that code")
	ErrRoomFull    = errors.New("the voice room is full")
)

// ICEServer is one RTCIceServer, in the shape WebRTC takes it.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Config is what a deployment sets.
type Config struct {
	// TURNSecret is coturn's static-auth-secret (--use-auth-secret): the
	// credentials handed out are the TURN REST API's time-limited pair,
	// username "<expiry>:<account>" and password base64(HMAC-SHA1(secret,
	// username)). Empty hands out no TURN servers.
	TURNSecret string
	TURNURLs   []string // turn:host:3478?transport=udp
	STUNURLs   []string // stun:host:3478
	// CredentialTTL is how long a TURN credential works; a call longer than
	// that keeps its allocation, only a new one needs a fresh credential.
	CredentialTTL time.Duration
}

// Room is what a client is told about a room.
type Room struct {
	Code  string `json:"code"`
	Kind  string `json:"kind"`            // "game" or "code"
	Title string `json:"title,omitempty"` // the title server's name for the game
}

// Peer is one connected phone.
type Peer struct {
	ID       string `json:"id"` // the account id
	Name     string `json:"name"`
	Muted    bool   `json:"muted"`
	Speaking bool   `json:"speaking"`
}

// Hub holds every room. In memory on purpose: a room is as live as the
// sockets in it, and a restart drops the sockets anyway (the app reconnects).
type Hub struct {
	cfg Config
	key []byte // derives game-room codes; per process, like the rooms

	mu    sync.Mutex
	rooms map[string]*room // by code
}

type room struct {
	Room
	peers      map[string]*conn
	emptySince time.Time
}

type conn struct {
	peer Peer
	out  chan []byte
	done chan struct{}
	once sync.Once
}

func (c *conn) close() { c.once.Do(func() { close(c.done) }) }

// New builds a hub.
func New(cfg Config) *Hub {
	if cfg.CredentialTTL <= 0 {
		cfg.CredentialTTL = 12 * time.Hour
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return &Hub{cfg: cfg, key: key, rooms: map[string]*room{}}
}

// ICEServers returns the STUN servers and a TURN credential for user.
func (h *Hub) ICEServers(user string, now time.Time) []ICEServer {
	out := []ICEServer{}
	if len(h.cfg.STUNURLs) > 0 {
		out = append(out, ICEServer{URLs: h.cfg.STUNURLs})
	}
	if h.cfg.TURNSecret != "" && len(h.cfg.TURNURLs) > 0 {
		name := fmt.Sprintf("%d:%s", now.Add(h.cfg.CredentialTTL).Unix(), user)
		mac := hmac.New(sha1.New, []byte(h.cfg.TURNSecret))
		mac.Write([]byte(name))
		out = append(out, ICEServer{URLs: h.cfg.TURNURLs, Username: name,
			Credential: base64.StdEncoding.EncodeToString(mac.Sum(nil))})
	}
	return out
}

// codeAlphabet leaves out 0/O and 1/I: the code is read off a TV and typed
// on a phone.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func encodeCode(b []byte) string {
	var s strings.Builder
	for i := range 6 {
		s.WriteByte(codeAlphabet[int(b[i])%len(codeAlphabet)])
	}
	return s.String()
}

// NormalizeCode reads a code the way a person types it.
func NormalizeCode(s string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, strings.TrimSpace(s)))
}

// GameRoom returns the room of one gathering, making it on first sight. Its
// code is derived from the gathering, so the app and the TV compute the same
// one without either storing it.
func (h *Hub) GameRoom(title string, gid uint32) Room {
	key := fmt.Sprintf("%s:%d", title, gid)
	mac := hmac.New(sha256.New, h.key)
	mac.Write([]byte(key))
	code := encodeCode(mac.Sum(nil))
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sweep(time.Now())
	if r, ok := h.rooms[code]; ok {
		return r.Room
	}
	r := &room{Room: Room{Code: code, Kind: "game", Title: title}, peers: map[string]*conn{}, emptySince: time.Now()}
	h.rooms[code] = r
	return r.Room
}

// CreateRoom makes a code room.
func (h *Hub) CreateRoom() Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sweep(time.Now())
	for {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		code := encodeCode(b)
		if _, taken := h.rooms[code]; taken {
			continue
		}
		r := &room{Room: Room{Code: code, Kind: "code"}, peers: map[string]*conn{}, emptySince: time.Now()}
		h.rooms[code] = r
		return r.Room
	}
}

// Lookup returns a room and who is connected to it.
func (h *Hub) Lookup(code string) (Room, []Peer, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[NormalizeCode(code)]
	if !ok {
		return Room{}, nil, false
	}
	return r.Room, r.peerList(""), true
}

// sweep forgets rooms that have been empty for idleRoomTTL. Called with mu
// held from the paths that make rooms, so the map cannot grow unattended.
func (h *Hub) sweep(now time.Time) {
	for code, r := range h.rooms {
		if len(r.peers) == 0 && now.Sub(r.emptySince) > idleRoomTTL {
			delete(h.rooms, code)
		}
	}
}

func (r *room) peerList(except string) []Peer {
	out := make([]Peer, 0, len(r.peers))
	for id, c := range r.peers {
		if id != except {
			out = append(out, c.peer)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// send queues a frame for c. A phone that stops reading is dropped rather
// than allowed to hold frames in memory: its app reconnects.
func (c *conn) send(b []byte) {
	select {
	case c.out <- b:
	case <-c.done:
	default:
		c.close()
	}
}

func (r *room) broadcast(except string, b []byte) {
	for id, c := range r.peers {
		if id != except {
			c.send(b)
		}
	}
}

// Join puts peer in the room with code and runs its socket until either side
// leaves. The same account joining again (a second phone, or the app
// reconnecting before the old socket timed out) replaces the first.
func (h *Hub) Join(ctx context.Context, code string, p Peer, ws *websocket.Conn) error {
	c := &conn{peer: p, out: make(chan []byte, 64), done: make(chan struct{})}
	h.mu.Lock()
	r, ok := h.rooms[NormalizeCode(code)]
	if !ok {
		h.mu.Unlock()
		return ErrUnknownRoom
	}
	if old, again := r.peers[p.ID]; again {
		old.close()
		delete(r.peers, p.ID)
		r.broadcast(p.ID, mustJSON(map[string]any{"type": "leave", "id": p.ID}))
	}
	if len(r.peers) >= MaxPeers {
		h.mu.Unlock()
		return ErrRoomFull
	}
	c.send(mustJSON(map[string]any{"type": "welcome", "self": p.ID, "name": p.Name, "room": r.Room,
		"peers": r.peerList(""), "ice_servers": h.ICEServers(p.ID, time.Now())}))
	r.broadcast(p.ID, mustJSON(map[string]any{"type": "join", "peer": p}))
	r.peers[p.ID] = c
	h.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		defer cancel()
		tick := time.NewTicker(pingEvery)
		defer tick.Stop()
		for {
			var b []byte
			select {
			case b = <-c.out:
			case <-tick.C:
				b = []byte(`{"type":"ping"}`)
			case <-c.done:
				return
			case <-ctx.Done():
				return
			}
			wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
			err := ws.Write(wctx, websocket.MessageText, b)
			wcancel()
			if err != nil {
				return
			}
		}
	}()
	go func() {
		select {
		case <-c.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	ws.SetReadLimit(maxFrame)
	for {
		_, b, err := ws.Read(ctx)
		if err != nil {
			break
		}
		h.handle(r, c, b)
	}

	h.mu.Lock()
	if r.peers[p.ID] == c {
		delete(r.peers, p.ID)
		r.broadcast(p.ID, mustJSON(map[string]any{"type": "leave", "id": p.ID}))
		if len(r.peers) == 0 {
			r.emptySince = time.Now()
		}
	}
	h.mu.Unlock()
	c.close()
	return nil
}

// handle acts on one frame from c. Unknown types are ignored, so an older
// server does not break a newer app.
func (h *Hub) handle(r *room, c *conn, b []byte) {
	var m struct {
		Type     string          `json:"type"`
		To       string          `json:"to"`
		Data     json.RawMessage `json:"data"`
		Muted    bool            `json:"muted"`
		Speaking bool            `json:"speaking"`
	}
	if json.Unmarshal(b, &m) != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.peers[c.peer.ID] != c {
		return // replaced
	}
	switch m.Type {
	case "signal":
		if to, ok := r.peers[m.To]; ok && m.To != c.peer.ID && len(m.Data) > 0 {
			to.send(mustJSON(map[string]any{"type": "signal", "from": c.peer.ID, "data": m.Data}))
		}
	case "state":
		if c.peer.Muted == m.Muted && c.peer.Speaking == m.Speaking {
			return
		}
		c.peer.Muted, c.peer.Speaking = m.Muted, m.Speaking && !m.Muted
		r.broadcast(c.peer.ID, mustJSON(map[string]any{"type": "state", "id": c.peer.ID,
			"muted": c.peer.Muted, "speaking": c.peer.Speaking}))
	}
}
