// Package supervision is the website's side of the parental controls core
// (platforms/core/infra/parental): account-level supervision (a parent and a child
// account) and the parent-controlled OpenVerse level. Two surfaces:
//
//   - gRPC GetProfile with the website adapter key, for the server-side questions
//     ("is this account supervised?" for Pakka and the Discord lookup);
//   - the core's HTTP API, with the shared key plus the signed-in person's session, for
//     the parental controls page.
//
// A nil *Client means parental controls are not configured (development). A configured
// client that cannot be reached fails closed: the account is treated as supervised.
package supervision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	pb "github.com/openpak/website/internal/parentalpb"
)

// OpenVerse levels a parent can set.
const (
	LevelOff      = "off"
	LevelRead     = "read"
	LevelReadPost = "read_post"
)

// Levels is the order the page offers them in.
var Levels = []string{LevelOff, LevelRead, LevelReadPost}

// ValidLevel reports whether l is one of the three levels.
func ValidLevel(l string) bool { return l == LevelOff || l == LevelRead || l == LevelReadPost }

// ErrUnavailable is any failure to get an answer from the core.
var ErrUnavailable = errors.New("parental controls service unavailable")

// Error is a non-2xx answer of the core's HTTP API.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return fmt.Sprintf("parental: %d %s", e.Status, e.Msg) }

type Client struct {
	HTTPURL    string // http://openpak-parental:20170
	WebKey     string // PARENTAL_INTERNAL_KEY
	AdapterKey string // the "website" adapter key
	HTTP       *http.Client

	grpcAddr string
	conn     *grpc.ClientConn
	rpc      pb.ParentalClient
}

// New returns nil when grpcAddr is empty (not configured).
func New(grpcAddr, httpURL, webKey, adapterKey string) (*Client, error) {
	if grpcAddr == "" {
		return nil, nil
	}
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{HTTPURL: httpURL, WebKey: webKey, AdapterKey: adapterKey, grpcAddr: grpcAddr, conn: conn,
		rpc: pb.NewParentalClient(conn), HTTP: &http.Client{Timeout: 10 * time.Second}}, nil
}

// Profile is what the core says about one account.
type Profile struct {
	Supervised bool
	OpenVerse  string // effective level; read_post for an account nobody supervises
}

// Lookup asks the core about accountID.
func (c *Client) Lookup(ctx context.Context, accountID string) (Profile, error) {
	if c == nil {
		return Profile{OpenVerse: LevelReadPost}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "x-internal-key", c.AdapterKey)
	res, err := c.rpc.GetProfile(ctx, &pb.GetProfileRequest{MemberId: accountID})
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return Profile{Supervised: res.GetSupervised(), OpenVerse: res.GetOpenverse()}, nil
}

// SupervisedOrUnknown is the fail-closed question: true when the account is supervised
// or when the core could not say. A nil client (not configured) says false.
func (c *Client) SupervisedOrUnknown(ctx context.Context, accountID string) bool {
	if c == nil {
		return false
	}
	p, err := c.Lookup(ctx, accountID)
	return err != nil || p.Supervised
}

// ---- HTTP, on behalf of a signed-in person ----

// Row is one supervision row of /v1/me/supervision.
type Row struct {
	MemberID     string `json:"member_id"`
	SupervisorID string `json:"supervisor_id"`
	Role         string `json:"role"`
	State        string `json:"state"`
}

// Mine is the caller's own supervision, both directions.
type Mine struct {
	SupervisedBy []Row `json:"supervised_by"`
	Supervising  []Row `json:"supervising"`
}

func (c *Client) do(ctx context.Context, session, method, path string, in, out any) error {
	if c == nil || c.HTTPURL == "" {
		return ErrUnavailable
	}
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.HTTPURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Key", c.WebKey)
	req.Header.Set("Authorization", "Bearer "+session)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &Error{Status: res.StatusCode, Msg: e.Error}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *Client) Mine(ctx context.Context, session string) (Mine, error) {
	var m Mine
	err := c.do(ctx, session, "GET", "/v1/me/supervision", nil, &m)
	return m, err
}

// Invite makes the caller a supervisor of member (pending until they accept, or active at
// once for an account under 13).
func (c *Client) Invite(ctx context.Context, session, member string) (state string, err error) {
	var out struct {
		State string `json:"state"`
	}
	err = c.do(ctx, session, "POST", "/v1/me/members", map[string]string{"member_id": member}, &out)
	return out.State, err
}

func (c *Client) Accept(ctx context.Context, session, member, supervisor string) error {
	return c.do(ctx, session, "POST", "/v1/me/supervisions/"+url.PathEscape(member)+"/"+url.PathEscape(supervisor)+"/accept", nil, nil)
}

func (c *Client) Remove(ctx context.Context, session, member, supervisor string) error {
	return c.do(ctx, session, "DELETE", "/v1/me/supervisions/"+url.PathEscape(member)+"/"+url.PathEscape(supervisor), nil, nil)
}

// OpenVerseLevel reads the member's stored level: off when none was ever set.
func (c *Client) OpenVerseLevel(ctx context.Context, session, member string) (level string, version uint64, err error) {
	var out struct {
		Profile map[string]json.RawMessage `json:"profile"`
		Version uint64                     `json:"version"`
	}
	if err = c.do(ctx, session, "GET", "/v1/me/members/"+url.PathEscape(member)+"/profile", nil, &out); err != nil {
		return "", 0, err
	}
	level = LevelOff
	if raw, ok := out.Profile["openverse"]; ok {
		var l string
		if json.Unmarshal(raw, &l) == nil && ValidLevel(l) {
			level = l
		}
	}
	return level, out.Version, nil
}

// SetOpenVerseLevel stores the level, keeping every other key of the member's profile (the
// profile is replaced whole, so the stored document is read first and sent back with the
// version, which makes a concurrent edit a 409 instead of a lost update).
func (c *Client) SetOpenVerseLevel(ctx context.Context, session, member, level string) error {
	if !ValidLevel(level) {
		return &Error{Status: http.StatusBadRequest, Msg: "bad level"}
	}
	var cur struct {
		Profile map[string]json.RawMessage `json:"profile"`
		Version uint64                     `json:"version"`
	}
	if err := c.do(ctx, session, "GET", "/v1/me/members/"+url.PathEscape(member)+"/profile", nil, &cur); err != nil {
		return err
	}
	if cur.Profile == nil {
		cur.Profile = map[string]json.RawMessage{}
	}
	cur.Profile["openverse"], _ = json.Marshal(level)
	return c.do(ctx, session, "PUT", "/v1/me/members/"+url.PathEscape(member)+"/profile",
		map[string]any{"profile": cur.Profile, "version": cur.Version}, nil)
}
