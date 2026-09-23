package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/openpak/website/internal/metrics"
	"github.com/openpak/website/internal/voice"
)

// titleServer fakes switch-nex's gathering lookup: acct-a@x.test and a friend
// are in gathering 77 of splatoon-2; a third console is there on an unproven
// login.
func titleServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/voice/gathering" || r.Header.Get("X-Internal-Key") != "nx-key" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("account_id") != "acct-a@x.test" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"title":"splatoon-2","gid":77,"participants":[
			{"pid":1,"account_id":"acct-a@x.test","nickname":"Ink A"},
			{"pid":2,"account_id":"acct-b@x.test","nickname":"Ink B"},
			{"pid":3,"account_id":"","nickname":""}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVoiceFindsTheGameRoomAndMakesCodeRooms(t *testing.T) {
	s, _, _ := newTestAPI(t)
	s.Gatherings = voice.NewGatherings([]string{titleServer(t).URL}, "nx-key")
	tok := signIn(t, s)

	rec := do(t, s, http.MethodGet, "/api/v1/me/voice", tok, nil)
	var cur struct {
		Room *struct {
			Code, Kind, Title string
			MaxMembers        int `json:"max_members"`
			Members           []struct {
				AccountID   string `json:"account_id"`
				DisplayName string `json:"display_name"`
				InVoice     bool   `json:"in_voice"`
			}
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cur); err != nil || rec.Code != http.StatusOK || cur.Room == nil {
		t.Fatalf("current: %d %s", rec.Code, rec.Body.String())
	}
	if cur.Room.Kind != "game" || cur.Room.Title != "splatoon-2" || cur.Room.MaxMembers != 8 || len(cur.Room.Code) != 6 {
		t.Fatalf("room %+v", cur.Room)
	}
	// The unproven console is nobody: it cannot be listed.
	if len(cur.Room.Members) != 2 || cur.Room.Members[1].DisplayName != "Ink B" || cur.Room.Members[0].InVoice {
		t.Fatalf("members %+v", cur.Room.Members)
	}
	// Asked again, the same gathering is the same room.
	again := do(t, s, http.MethodGet, "/api/v1/me/voice", tok, nil)
	if !strings.Contains(again.Body.String(), `"code":"`+cur.Room.Code+`"`) {
		t.Fatalf("the code moved: %s", again.Body.String())
	}

	rec = do(t, s, http.MethodPost, "/api/v1/me/voice/rooms", tok, nil)
	var made struct{ Room struct{ Code, Kind string } }
	_ = json.Unmarshal(rec.Body.Bytes(), &made)
	if rec.Code != http.StatusCreated || made.Room.Kind != "code" || made.Room.Code == "" {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, http.MethodGet, "/api/v1/me/voice/rooms/"+strings.ToLower(made.Room.Code), tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("lookup by code: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, s, http.MethodGet, "/api/v1/me/voice/rooms/ZZZZZZ", tok, nil)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"voice_room_unknown"`) {
		t.Fatalf("unknown code: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, http.MethodGet, "/api/v1/me/voice", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: %d", rec.Code)
	}

	// No title server configured: there is simply no game room.
	s.Gatherings = nil
	if rec := do(t, s, http.MethodGet, "/api/v1/me/voice", tok, nil); !strings.Contains(rec.Body.String(), `"room":null`) {
		t.Fatalf("without a lookup: %s", rec.Body.String())
	}
}

// The lobby page's data, through nx-baas: the internal key, an account, the room.
func TestInternalVoiceRoomForTheLobby(t *testing.T) {
	t.Setenv("WEBSITE_INTERNAL_KEY", "k")
	s, _, _ := newTestAPI(t)
	s.Gatherings = voice.NewGatherings([]string{titleServer(t).URL}, "nx-key")
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	ask := func(account, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/internal/voice/room?app_id=01003BC0000A0000&account_id="+account, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := ask("acct-a@x.test", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", rec.Code)
	}
	rec := ask("acct-a@x.test", "k")
	var got struct {
		Code    string
		Players int `json:"players"`
		InVoice int `json:"in_voice"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || len(got.Code) != 6 || got.Players != 3 || got.InVoice != 0 {
		t.Fatalf("lobby room: %d %s", rec.Code, rec.Body.String())
	}
	if rec := ask("acct-b@x.test", "k"); rec.Code != http.StatusNotFound {
		t.Fatalf("an account in no game: %d", rec.Code)
	}
}

// The socket end to end, through the same middleware production wraps the
// mux in (a writer without Unwrap cannot be hijacked), with the bearer as a
// browser sends it.
func TestVoiceSocketUpgradesThroughTheMiddleware(t *testing.T) {
	s, _, _ := newTestAPI(t)
	tok := signIn(t, s)
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	srv := httptest.NewServer(metrics.New().Middleware(mux))
	defer srv.Close()
	room := s.Voice.CreateRoom()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/me/voice/ws?code=" + room.Code
	if _, resp, err := websocket.Dial(ctx, wsURL, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no bearer was let in: %v", err)
	}
	ws, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{Subprotocols: []string{"openpak-voice", "bearer." + tok}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.CloseNow()
	if resp.Header.Get("Sec-WebSocket-Protocol") != "openpak-voice" {
		t.Fatalf("subprotocol %q: the bearer must never be echoed back", resp.Header.Get("Sec-WebSocket-Protocol"))
	}
	_, b, err := ws.Read(ctx)
	if err != nil || !strings.Contains(string(b), `"type":"welcome"`) || !strings.Contains(string(b), `"self":"acct-a@x.test"`) {
		t.Fatalf("welcome: %s %v", b, err)
	}
	if _, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/me/voice/ws?code=NOPE22",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}}); err == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown room: %v", err)
	}
}
