package voice

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// phone is one test client on the signaling socket.
type phone struct {
	t  *testing.T
	ws *websocket.Conn
}

// serve runs h's Join for whoever the ?id= names, the way the API handler does.
func serve(t *testing.T, h *Hub) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		id := r.URL.Query().Get("id")
		if err := h.Join(r.Context(), r.URL.Query().Get("code"), Peer{ID: id, Name: "name-" + id}, ws); err != nil {
			b, _ := json.Marshal(map[string]string{"type": "error", "message": err.Error()})
			_ = ws.Write(context.Background(), websocket.MessageText, b)
			ws.Close(websocket.StatusPolicyViolation, "")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func dial(t *testing.T, srv *httptest.Server, code, id string) *phone {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/?code="+code+"&id="+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return &phone{t: t, ws: ws}
}

// next reads the next frame that is not a ping.
func (p *phone) next() map[string]any {
	p.t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, b, err := p.ws.Read(ctx)
		cancel()
		if err != nil {
			p.t.Fatalf("read: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			p.t.Fatal(err)
		}
		if m["type"] != "ping" {
			return m
		}
	}
}

func (p *phone) send(v any) {
	p.t.Helper()
	b, _ := json.Marshal(v)
	if err := p.ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		p.t.Fatal(err)
	}
}

func expect(t *testing.T, m map[string]any, typ string) map[string]any {
	t.Helper()
	if m["type"] != typ {
		t.Fatalf("got %v, want a %q frame", m, typ)
	}
	return m
}

func TestTwoPhonesMeetSignalAndLeave(t *testing.T) {
	h := New(Config{TURNSecret: "s3cret", TURNURLs: []string{"turn:turn.test:3478?transport=udp"}, STUNURLs: []string{"stun:turn.test:3478"}})
	room := h.CreateRoom()
	srv := serve(t, h)

	a := dial(t, srv, room.Code, "a")
	w := expect(t, a.next(), "welcome")
	if w["self"] != "a" || len(w["peers"].([]any)) != 0 || w["room"].(map[string]any)["code"] != room.Code {
		t.Fatalf("first welcome: %v", w)
	}
	if ice := w["ice_servers"].([]any); len(ice) != 2 {
		t.Fatalf("ice servers: %v", ice)
	}

	// The code is typed on a phone: case and separators do not matter.
	b := dial(t, srv, strings.ToLower(room.Code[:3])+"-"+room.Code[3:], "b")
	w = expect(t, b.next(), "welcome")
	peers := w["peers"].([]any)
	if len(peers) != 1 || peers[0].(map[string]any)["id"] != "a" {
		t.Fatalf("second welcome lists %v, want a — the newcomer offers to everyone listed", peers)
	}
	if j := expect(t, a.next(), "join"); j["peer"].(map[string]any)["id"] != "b" {
		t.Fatalf("a was told %v", j)
	}

	// Offer, answer and a candidate are relayed to the named peer only, marked with the sender.
	b.send(map[string]any{"type": "signal", "to": "a", "data": map[string]any{"sdp": map[string]string{"type": "offer", "sdp": "v=0"}}})
	s := expect(t, a.next(), "signal")
	if s["from"] != "b" || s["data"].(map[string]any)["sdp"].(map[string]any)["type"] != "offer" {
		t.Fatalf("relayed %v", s)
	}
	a.send(map[string]any{"type": "signal", "to": "b", "data": map[string]any{"sdp": map[string]string{"type": "answer", "sdp": "v=0"}}})
	if s := expect(t, b.next(), "signal"); s["from"] != "a" {
		t.Fatalf("answer relayed as %v", s)
	}
	// To nobody in the room: dropped, and nothing else breaks.
	a.send(map[string]any{"type": "signal", "to": "zed", "data": map[string]any{"candidate": "x"}})

	a.send(map[string]any{"type": "state", "muted": true, "speaking": true})
	st := expect(t, b.next(), "state")
	if st["id"] != "a" || st["muted"] != true || st["speaking"] != false {
		t.Fatalf("state %v: a muted phone is never speaking", st)
	}
	if _, peers, _ := h.Lookup(room.Code); len(peers) != 2 {
		t.Fatalf("lookup sees %d peers", len(peers))
	}

	a.ws.Close(websocket.StatusNormalClosure, "")
	if l := expect(t, b.next(), "leave"); l["id"] != "a" {
		t.Fatalf("leave %v", l)
	}
}

func TestTheRoomHoldsEightAndARejoinReplaces(t *testing.T) {
	h := New(Config{})
	room := h.CreateRoom()
	srv := serve(t, h)
	var first *phone
	for i := range MaxPeers {
		p := dial(t, srv, room.Code, fmt.Sprint(i))
		expect(t, p.next(), "welcome")
		if i == 0 {
			first = p
		}
	}
	extra := dial(t, srv, room.Code, "nine")
	if m := extra.next(); m["type"] != "error" || m["message"] != ErrRoomFull.Error() {
		t.Fatalf("ninth phone got %v", m)
	}

	// Account 0 reconnects (the app came back before its old socket timed
	// out): the new socket takes the place, it does not need a ninth seat.
	again := dial(t, srv, room.Code, "0")
	if w := expect(t, again.next(), "welcome"); len(w["peers"].([]any)) != MaxPeers-1 {
		t.Fatalf("rejoin sees %d peers", len(w["peers"].([]any)))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := first.ws.Read(ctx); err != nil {
			break // the replaced socket is closed
		}
	}
}

func TestUnknownCodesAndGameRoomCodes(t *testing.T) {
	h := New(Config{})
	if err := h.Join(context.Background(), "NOPE22", Peer{ID: "x"}, nil); !errors.Is(err, ErrUnknownRoom) {
		t.Fatalf("unknown code: %v", err)
	}
	r1 := h.GameRoom("splatoon-2", 7001)
	r2 := h.GameRoom("splatoon-2", 7001)
	r3 := h.GameRoom("splatoon-2", 7002)
	if r1 != r2 || r1.Code == r3.Code || r1.Kind != "game" || r1.Title != "splatoon-2" {
		t.Fatalf("game rooms %v %v %v: one gathering, one stable code", r1, r2, r3)
	}
	if len(r1.Code) != 6 || strings.ContainsAny(r1.Code, "01IO") {
		t.Fatalf("code %q is not six unambiguous characters", r1.Code)
	}
	if _, _, ok := h.Lookup(r1.Code); !ok {
		t.Fatal("a game room is not joinable by its code")
	}
}

// The credential coturn checks with --use-auth-secret: username
// "<expiry>:<user>", password base64(HMAC-SHA1(secret, username)).
func TestTURNCredentialsAreTheRESTShape(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	h := New(Config{TURNSecret: "s3cret", TURNURLs: []string{"turn:t:3478"}, CredentialTTL: time.Hour})
	ice := h.ICEServers("acct-1", now)
	if len(ice) != 1 {
		t.Fatalf("%v", ice)
	}
	if want := fmt.Sprintf("%d:acct-1", now.Add(time.Hour).Unix()); ice[0].Username != want {
		t.Fatalf("username %q, want %q", ice[0].Username, want)
	}
	mac := hmac.New(sha1.New, []byte("s3cret"))
	mac.Write([]byte(ice[0].Username))
	if ice[0].Credential != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
		t.Fatal("credential is not HMAC-SHA1 of the username")
	}
	if len(New(Config{}).ICEServers("x", now)) != 0 {
		t.Fatal("no TURN configured still handed out a server")
	}
}

func TestGatheringsAsksEachTitleServer(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer down.Close()
	var gotKey string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Internal-Key")
		if r.URL.Query().Get("account_id") != "acct-1" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"title":"splatoon-2","gid":9,"participants":[{"pid":1,"account_id":"acct-1","nickname":"Ink"}]}`)
	}))
	defer up.Close()

	g := NewGatherings([]string{down.URL, up.URL + "/"}, "k")
	got, err := g.Of(context.Background(), "acct-1")
	if err != nil || got == nil || got.Title != "splatoon-2" || got.GID != 9 || got.Participants[0].Nickname != "Ink" || gotKey != "k" {
		t.Fatalf("Of = %+v, %v (key %q)", got, err, gotKey)
	}
	if got, err := g.Of(context.Background(), "acct-2"); got != nil || err == nil {
		t.Fatalf("not found anywhere with one server down = %+v, %v; want nil and the error", got, err)
	}
	if NewGatherings([]string{" ", ""}, "k") != nil {
		t.Fatal("no URLs should mean no lookup")
	}
}

// One phone flooding the room is capped, so it cannot fill another phone's
// queue and get that phone dropped.
func TestAFloodIsCappedNotRelayed(t *testing.T) {
	h := New(Config{})
	room := h.CreateRoom()
	srv := serve(t, h)
	a := dial(t, srv, room.Code, "a")
	expect(t, a.next(), "welcome")
	b := dial(t, srv, room.Code, "b")
	expect(t, b.next(), "welcome")
	expect(t, a.next(), "join")

	for i := range 200 {
		a.send(map[string]any{"type": "state", "muted": i%2 == 0})
	}
	time.Sleep(300 * time.Millisecond)
	if _, peers, _ := h.Lookup(room.Code); len(peers) != 2 {
		t.Fatalf("the flood cost a phone its seat: %d left", len(peers))
	}
	// (A read that runs out its context closes the socket, so the seat is checked first.)
	got := 0
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	for {
		_, msg, err := b.ws.Read(ctx)
		if err != nil {
			break
		}
		if strings.Contains(string(msg), `"type":"state"`) {
			got++
		}
	}
	if got == 0 || got > 2*maxPerSecond {
		t.Fatalf("b was sent %d state frames out of 200; the cap is %d a second", got, maxPerSecond)
	}
}
