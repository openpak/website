package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/openpak/website/internal/store"
)

func mintTokenForTest(t *testing.T, s *Server) string {
	t.Helper()
	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint token: %d %s", rec.Code, rec.Body.String())
	}
	var m struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return m.Token
}

// GET is public (voted=false anon), voting needs a bearer and toggles.
func TestRequestsListAndVote(t *testing.T) {
	s, _, db := newTestAPI(t)
	ctx := context.Background()
	if err := db.SeedRequests(ctx, []store.Request{
		{Kind: "title", Name: "Tetris 99", Console: "Switch", Status: "requestable", Sort: 1},
		{Kind: "feature", Name: "Cloud saves", Console: "Switch", Status: "planned", Sort: 2},
	}); err != nil {
		t.Fatal(err)
	}
	reqs, _ := db.ListRequests(ctx, "title", "")
	id := reqs[0].ID

	// Anonymous list: items present, voted false, votes zero.
	rec := do(t, s, http.MethodGet, "/api/v1/requests", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("anon list: %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []struct {
			ID    int64  `json:"id"`
			Name  string `json:"name"`
			Votes int    `json:"votes"`
			Voted bool   `json:"voted"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("anon list: %d items, want 2", len(list.Items))
	}
	for _, it := range list.Items {
		if it.Voted || it.Votes != 0 {
			t.Errorf("anon %q: votes=%d voted=%v, want 0/false", it.Name, it.Votes, it.Voted)
		}
	}

	// Kind filter narrows.
	rec = do(t, s, http.MethodGet, "/api/v1/requests?kind=feature", "", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Name != "Cloud saves" {
		t.Fatalf("kind=feature: %+v", list.Items)
	}

	// Voting without a bearer is rejected.
	path := "/api/v1/requests/" + strconv.FormatInt(id, 10) + "/vote"
	if rec := do(t, s, http.MethodPost, path, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon vote: %d, want 401", rec.Code)
	}

	bearer := mintTokenForTest(t, s)

	// Vote → votes=1 voted=true, echoing the id.
	rec = do(t, s, http.MethodPost, path, bearer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("vote: %d %s", rec.Code, rec.Body.String())
	}
	var vr struct {
		ID    int64 `json:"id"`
		Votes int   `json:"votes"`
		Voted bool  `json:"voted"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &vr)
	if vr.ID != id || vr.Votes != 1 || !vr.Voted {
		t.Fatalf("vote response: %+v, want id=%d votes=1 voted=true", vr, id)
	}

	// Authed list now shows voted=true for that item.
	rec = do(t, s, http.MethodGet, "/api/v1/requests", bearer, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	for _, it := range list.Items {
		if it.ID == id && (!it.Voted || it.Votes != 1) {
			t.Errorf("authed list: id %d votes=%d voted=%v, want 1/true", id, it.Votes, it.Voted)
		}
	}

	// Toggle off.
	rec = do(t, s, http.MethodPost, path, bearer, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &vr)
	if vr.Votes != 0 || vr.Voted {
		t.Fatalf("unvote response: %+v, want votes=0 voted=false", vr)
	}

	// Voting a feature (never votable) is refused server-side with 409.
	feats, _ := db.ListRequests(ctx, "feature", "")
	featPath := "/api/v1/requests/" + strconv.FormatInt(feats[0].ID, 10) + "/vote"
	if rec := do(t, s, http.MethodPost, featPath, bearer, nil); rec.Code != http.StatusConflict {
		t.Fatalf("feature vote: %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if v, _, _ := db.RequestVotes(ctx, feats[0].ID, "acct-a"); v != 0 {
		t.Errorf("refused feature vote still recorded %d votes", v)
	}
}
