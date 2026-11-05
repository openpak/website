package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Client self-update mirror. A console tool whose TLS stack cannot reach GitHub or openpak.org
// (the PS3's 4.93 stack: RSA key exchange only) fetches its update from here over plain HTTP.
// Transport is not the trust: the release manifest is signed with the release-signing key the
// client pins, and names the sha256 of the package, so this endpoint only relays bytes.
//
//	GET /api/v1/clients/{client}/latest          manifest.txt of the newest release
//	GET /api/v1/clients/{client}/latest.sig      manifest.sig, hex Ed25519 signature over those bytes
//	GET /api/v1/clients/{client}/download/{file} the package the manifest names
//
// The release is read from the public GitHub repository at request time and cached; a new
// release needs no deploy and no mirroring step.

// ClientUpdateRepos maps the {client} path segment to its GitHub repository and the exact name
// pattern of the package asset it may serve.
var clientUpdateRepos = map[string]struct {
	Repo string
	File *regexp.Regexp
}{
	"ps3-patcher": {"openpak/ps3-patcher", regexp.MustCompile(`^OpenPak-PS3-Patcher-v[0-9]+\.[0-9]+\.[0-9]+\.pkg$`)},
}

const (
	clientUpdateTTL   = 10 * time.Minute
	clientUpdateStale = 24 * time.Hour
	clientUpdateMax   = 16 << 20
)

// ClientUpdates reads the newest GitHub release of a client repository, with a cache.
type ClientUpdates struct {
	// APIBase is the GitHub API root; tests point it at a fake.
	APIBase string
	HTTP    *http.Client

	mu    sync.Mutex
	state map[string]*clientRelease
}

type clientRelease struct {
	tag     string
	fetched time.Time
	assets  map[string]string // name -> download URL
	bodies  map[string][]byte // fetched assets, by name
}

func NewClientUpdates() *ClientUpdates {
	return &ClientUpdates{APIBase: "https://api.github.com", HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (c *ClientUpdates) release(ctx context.Context, repo string) (*clientRelease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == nil {
		c.state = map[string]*clientRelease{}
	}
	cur := c.state[repo]
	if cur != nil && time.Since(cur.fetched) < clientUpdateTTL {
		return cur, nil
	}
	rel, err := c.fetchRelease(ctx, repo)
	if err != nil {
		if cur != nil && time.Since(cur.fetched) < clientUpdateStale {
			return cur, nil
		}
		return nil, err
	}
	if cur != nil && cur.tag == rel.tag {
		cur.fetched = time.Now() // same release: keep the fetched asset bodies
		return cur, nil
	}
	c.state[repo] = rel
	return rel, nil
}

func (c *ClientUpdates) fetchRelease(ctx context.Context, repo string) (*clientRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBase+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "openpak-website")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases/latest: %s", resp.Status)
	}
	var doc struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, err
	}
	rel := &clientRelease{tag: doc.Tag, fetched: time.Now(), assets: map[string]string{}, bodies: map[string][]byte{}}
	for _, a := range doc.Assets {
		rel.assets[a.Name] = a.URL
	}
	return rel, nil
}

// asset returns one release asset's bytes, fetching and keeping them on first use.
func (c *ClientUpdates) asset(ctx context.Context, repo, name string) ([]byte, error) {
	rel, err := c.release(ctx, repo)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if b, ok := rel.bodies[name]; ok {
		c.mu.Unlock()
		return b, nil
	}
	url, ok := rel.assets[name]
	c.mu.Unlock()
	if !ok {
		return nil, errAssetMissing
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "openpak-website")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github asset %s: %s", name, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, clientUpdateMax+1))
	if err != nil {
		return nil, err
	}
	if len(b) > clientUpdateMax {
		return nil, fmt.Errorf("asset %s too large", name)
	}
	c.mu.Lock()
	rel.bodies[name] = b
	c.mu.Unlock()
	return b, nil
}

var errAssetMissing = fmt.Errorf("release asset missing")

func (s *Server) registerClientUpdates(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/clients/{client}/latest", s.handleClientAsset("manifest.txt"))
	mux.HandleFunc("GET /api/v1/clients/{client}/latest.sig", s.handleClientAsset("manifest.sig"))
	mux.HandleFunc("GET /api/v1/clients/{client}/download/{file}", s.handleClientAsset(""))
}

func (s *Server) handleClientAsset(fixed string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := clientUpdateRepos[r.PathValue("client")]
		if !ok {
			writeError(w, http.StatusNotFound, "unknown client")
			return
		}
		if s.ClientUpdates == nil {
			writeError(w, http.StatusServiceUnavailable, "client updates are not configured")
			return
		}
		name := fixed
		if name == "" {
			name = r.PathValue("file")
			if !cfg.File.MatchString(name) {
				writeError(w, http.StatusNotFound, "unknown file")
				return
			}
		}
		body, err := s.ClientUpdates.asset(r.Context(), cfg.Repo, name)
		if err != nil {
			if err == errAssetMissing {
				writeError(w, http.StatusNotFound, "no such release asset")
				return
			}
			s.log.Error("client update", "client", r.PathValue("client"), "err", err)
			writeError(w, http.StatusBadGateway, "release unavailable")
			return
		}
		ct := "application/octet-stream"
		if strings.HasPrefix(name, "manifest.") {
			ct = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}
