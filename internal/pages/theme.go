package pages

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openpak/website/internal/rewards"
)

// Themes. A Rewards theme is a JSON asset in the "openpak-theme/1" schema: a set of colour tokens for light and
// one for dark, named like the custom properties in web/src/input.css. The signed-in account's equipped theme
// is written into every page as a <style> (its hash is added to the CSP, so nothing else can be injected), and
// /themes/{id} serves the same validated tokens for any shop theme so a page can try one on without a reload.
//
// The asset is not trusted: every value must be a strict CSS colour before it reaches a page or a response.

const themeSchema = "openpak-theme/1"

// themeTokens are the tokens a theme may set, each the name of a custom property. The selection cursor is not
// one: it stays on every theme.
var themeTokens = []string{
	"paper", "paper-2", "raised", "sunk", "ink", "ink-2", "muted", "faint", "line", "line-2",
	"accent", "accent-2", "accent-weak", "accent-line", "on-accent",
	"ok", "ok-weak", "ok-line", "bad", "bad-weak", "bad-line", "warn", "warn-weak", "warn-line",
}

// themeRequired are the tokens a mode must have for the theme to be used at all.
var themeRequired = []string{"paper", "ink", "accent", "on-accent"}

var (
	hexColour = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	rgbColour = regexp.MustCompile(`^rgba?\(\s*(?:25[0-5]|2[0-4]\d|1?\d?\d)\s*,\s*(?:25[0-5]|2[0-4]\d|1?\d?\d)\s*,\s*(?:25[0-5]|2[0-4]\d|1?\d?\d)\s*(?:,\s*(?:0|1|0?\.\d{1,4}|1\.0{1,4})\s*)?\)$`)
	themeID   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)
)

// cssColour reports whether v is a hex colour or an rgb()/rgba() with comma-separated numbers, and nothing else.
func cssColour(v string) bool {
	return len(v) <= 40 && (hexColour.MatchString(v) || rgbColour.MatchString(v))
}

// Theme is a validated theme: only known tokens, only colours.
type Theme struct {
	ID, Name    string
	Light, Dark map[string]string
}

var errTheme = errors.New("theme: not a usable openpak-theme/1 asset")

// parseTheme validates an asset. A value that is not a strict colour, or a token that is not known, is dropped;
// a theme missing a required token in either mode, or with another schema, is rejected whole.
func parseTheme(raw []byte) (Theme, error) {
	var in struct {
		Schema string                       `json:"schema"`
		ID     string                       `json:"id"`
		Name   string                       `json:"name"`
		Tokens map[string]map[string]string `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.Schema != themeSchema {
		return Theme{}, errTheme
	}
	t := Theme{ID: in.ID, Name: in.Name}
	for _, mode := range []string{"light", "dark"} {
		out := map[string]string{}
		for _, k := range themeTokens {
			if v := strings.TrimSpace(in.Tokens[mode][k]); cssColour(v) {
				out[k] = strings.ToLower(v)
			}
		}
		for _, k := range themeRequired {
			if out[k] == "" {
				return Theme{}, errTheme
			}
		}
		if mode == "light" {
			t.Light = out
		} else {
			t.Dark = out
		}
	}
	return t, nil
}

func declarations(m map[string]string) string {
	var b strings.Builder
	for _, k := range themeTokens {
		if v, ok := m[k]; ok {
			b.WriteString("--" + k + ":" + v + ";")
		}
	}
	return b.String()
}

// CSS is the rule set for the layout's <style>: the light tokens, then the dark ones under the system setting.
func (t Theme) CSS() string {
	return ":root{" + declarations(t.Light) + "}@media (prefers-color-scheme:dark){:root{" + declarations(t.Dark) + "}}"
}

// themeView is what the layout prints for an account with a theme equipped.
type themeView struct {
	CSS         template.CSS
	Hash        string // 'sha256-...' for the CSP's style-src
	Light, Dark string // the paper colours, for <meta name="theme-color">
	// MarkMedia is the media query the console marks' dark <source> answers to on this theme: dark paper in
	// light mode, or light paper in dark mode, must not pick the file by the system setting alone.
	MarkMedia string
}

// paperIsDark reports whether a theme paper colour (hex or rgb) is dark, by relative luminance. Unknown is light.
func paperIsDark(c string) bool {
	var r, g, b float64
	switch {
	case strings.HasPrefix(c, "#") && (len(c) == 7 || len(c) == 9):
		var v [3]int
		for i := range v {
			n, err := strconv.ParseUint(c[1+2*i:3+2*i], 16, 8)
			if err != nil {
				return false
			}
			v[i] = int(n)
		}
		r, g, b = float64(v[0]), float64(v[1]), float64(v[2])
	case strings.HasPrefix(c, "#") && (len(c) == 4 || len(c) == 5):
		var v [3]int
		for i := range v {
			n, err := strconv.ParseUint(string([]byte{c[1+i], c[1+i]}), 16, 8)
			if err != nil {
				return false
			}
			v[i] = int(n)
		}
		r, g, b = float64(v[0]), float64(v[1]), float64(v[2])
	case strings.HasPrefix(c, "rgb"):
		f := strings.FieldsFunc(c[strings.Index(c, "(")+1:], func(r rune) bool { return r == ',' || r == ')' || r == ' ' })
		if len(f) < 3 {
			return false
		}
		r, _ = strconv.ParseFloat(f[0], 64)
		g, _ = strconv.ParseFloat(f[1], 64)
		b, _ = strconv.ParseFloat(f[2], 64)
	default:
		return false
	}
	return (0.2126*r+0.7152*g+0.0722*b)/255 < 0.5
}

// markMedia picks the dark mark's media query from which modes of the theme have dark paper.
func markMedia(lightPaper, darkPaper string) string {
	ld, dd := paperIsDark(lightPaper), paperIsDark(darkPaper)
	switch {
	case ld && dd:
		return "all"
	case ld:
		return "(prefers-color-scheme: light)"
	case !dd:
		return "not all"
	}
	return "(prefers-color-scheme: dark)"
}

func viewOfTheme(t Theme) *themeView {
	css := t.CSS()
	sum := sha256.Sum256([]byte(css))
	return &themeView{CSS: template.CSS(css), Hash: "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'",
		Light: t.Light["paper"], Dark: t.Dark["paper"], MarkMedia: markMedia(t.Light["paper"], t.Dark["paper"])}
}

// markMedia is read by render: "" when the default query already fits.
func (b baseData) markMedia() string {
	if b.Theme == nil || b.Theme.MarkMedia == "(prefers-color-scheme: dark)" {
		return ""
	}
	return b.Theme.MarkMedia
}

// styleHash is read by render: pages embed baseData, so every page answers it.
func (b baseData) styleHash() string {
	if b.Theme == nil {
		return ""
	}
	return b.Theme.Hash
}

// themeCache keeps what the layout needs on every page out of the rewards service: the equipped theme per account
// for a short while, the parsed assets, and the public theme list. Its zero value is ready to use.
type themeCache struct {
	mu     sync.Mutex
	themes map[string]themeEntry
	acct   map[string]acctEntry
	list   map[string]rewards.Item
	listAt time.Time
}

type themeEntry struct {
	t     *Theme // nil: no usable theme (cached briefly so a broken asset is not fetched on every page)
	until time.Time
}

type acctEntry struct {
	id    string
	until time.Time
}

const (
	acctTTL      = 45 * time.Second
	themeTTL     = 5 * time.Minute
	themeFailTTL = 30 * time.Second
	listTTL      = time.Minute
	themeWait    = 1500 * time.Millisecond // the layout never waits longer than this for rewards
)

// forget drops an account's cached theme: an equip or a purchase just changed it.
func (c *themeCache) forget(account string) {
	c.mu.Lock()
	delete(c.acct, account)
	c.mu.Unlock()
}

// theme returns the validated theme for an item id, fetched from the rewards service.
func (s *Server) themeAsset(ctx context.Context, id string) *Theme {
	c := &s.themes
	now := time.Now()
	c.mu.Lock()
	if e, ok := c.themes[id]; ok && now.Before(e.until) {
		c.mu.Unlock()
		return e.t
	}
	c.mu.Unlock()
	var t *Theme
	ttl := themeFailTTL
	if raw, err := s.Rewards.ItemAsset(ctx, id); err == nil {
		if p, err := parseTheme(raw); err == nil {
			t, ttl = &p, themeTTL
		}
	}
	c.mu.Lock()
	if c.themes == nil || len(c.themes) > 256 {
		c.themes = map[string]themeEntry{}
	}
	c.themes[id] = themeEntry{t: t, until: now.Add(ttl)}
	c.mu.Unlock()
	return t
}

// themeList is the shop's active themes by id. On a failure the last list is kept, or nil.
func (s *Server) themeList(ctx context.Context) map[string]rewards.Item {
	c := &s.themes
	c.mu.Lock()
	if c.list != nil && time.Since(c.listAt) < listTTL {
		l := c.list
		c.mu.Unlock()
		return l
	}
	c.mu.Unlock()
	items, err := s.Rewards.PublicCatalog(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.listAt = time.Now().Add(listTTL - themeFailTTL) // try again in 30 s
		return c.list
	}
	c.list = map[string]rewards.Item{}
	for _, it := range items {
		if it.Kind == "theme" {
			c.list[it.ID] = it
		}
	}
	c.listAt = time.Now()
	return c.list
}

// equippedTheme is the layout's theme for a signed-in account, or nil: nothing equipped, rewards down,
// slow or broken. It never fails the page.
func (s *Server) equippedTheme(ctx context.Context, account, session string) *themeView {
	if s.Rewards == nil {
		return nil
	}
	c := &s.themes
	now := time.Now()
	c.mu.Lock()
	e, ok := c.acct[account]
	c.mu.Unlock()
	if !ok || now.After(e.until) {
		ctx, cancel := context.WithTimeout(ctx, themeWait)
		defer cancel()
		me, err := s.Rewards.Me(ctx, session)
		e = acctEntry{until: now.Add(acctTTL)}
		if err == nil {
			e.id = me.Equipped["theme"]
		} else {
			e.until = now.Add(themeFailTTL)
		}
		c.mu.Lock()
		if c.acct == nil || len(c.acct) > 4096 {
			c.acct = map[string]acctEntry{}
		}
		c.acct[account] = e
		c.mu.Unlock()
	}
	if e.id == "" || !themeID.MatchString(e.id) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, themeWait)
	defer cancel()
	if t := s.themeAsset(ctx, e.id); t != nil {
		return viewOfTheme(*t)
	}
	return nil
}

// handleTheme serves GET /themes/{id}: the validated tokens of a shop theme, to anyone. Only what the public
// shop lists is served, and only validated tokens leave: the asset's own text never does.
func (s *Server) handleTheme(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.Rewards == nil || !themeID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	item, ok := s.themeList(ctx)[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	t := s.themeAsset(ctx, id)
	if t == nil {
		http.Error(w, "theme unavailable", http.StatusBadGateway)
		return
	}
	body, _ := json.Marshal(struct {
		ID    string            `json:"id"`
		Name  string            `json:"name"`
		Light map[string]string `json:"light"`
		Dark  map[string]string `json:"dark"`
	}{id, item.Name, t.Light, t.Dark})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
}
