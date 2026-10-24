// Command title-art builds the game tiles the /titles grid and the HOME shelf draw for
// every non-Switch title (Switch titles show their own icon through /titles/icon/{id}).
// Each tile is a 256×256 JPEG under web/static/art/titles, named by titleart.Slug, cut
// from the game's real published art; there is no generated or redrawn art.
//
// Input is titles.tsv next to this file, one row per title:
//
//	console  name  source URL  author  crop
//
// The source is either
//   - a SteamGridDB square grid (cdn2.steamgriddb.com, 512 or 1024 px), the first choice,
//     picked by hand from the -candidates sheet and credited to its author in the author
//     column; it is downscaled whole. Only grids that are the game's own art and title
//     logo, full bleed: nothing else on them (no platform marks or frames, publisher
//     logos, ratings, taglines, edition banners, borders or signatures), the console
//     release of the game, the right edition; or
//   - when no grid passes, the published front cover (box art): libretro-thumbnails
//     Named_Boxarts (Wii, DS, 3DS, Wii U, PS2, PSP, Xbox), GameTDB coverHQ (PS3), the
//     Xbox 360 marketplace boxartlg by title id (download.xbox.com), or a Wikipedia
//     infobox cover (Vita and the rest). The same whitelist holds: the crop must leave
//     out the platform band, ratings, publisher logos and badges, so a cover is cut by
//     the per-console insets in crops below, overridden per row by the crop column
//     ("t=0.1 l=0.12 y=0.2 z=0.9": insets as fractions of the cover, then where the
//     square sits in what remains, 0 top/left to 1 bottom/right, and z<1 zooms in).
//     A cover whose extras cannot be cropped out without cutting the logo gets no row; or
//   - file:<name>, a square of the game's official art kept in scripts/title-art/art/ when
//     neither source has one (same whitelist); it is downscaled whole like a grid.
//
// Run from the repository root:
//
//	go run ./scripts/title-art -cache ../scratch/title-art/cache -sheets ../scratch/title-art/sheets
//
// It writes the tiles and SOURCES.tsv (console, name, file, source, author) to -out,
// removes tiles no row produces any more, and with -sheets a labelled contact sheet per
// console: look at every one before publishing. Downloads are cached by URL in -cache.
//
// A new title: run with -candidates (needs STEAMGRIDDB_API_KEY; the key never goes in
// the repository) to fetch the catalogue, look the titles that have no row yet up on
// SteamGridDB and write a sheet of their acceptable grids (official/alternate style, no
// nsfw/humor/epilepsy) to -sheets, with candidates.tsv listing them. Check the matched
// game is the console release, pick a grid built from official art with the game's
// logo, and add its row; with nothing good, add the cover instead. Leave a title out
// rather than give it the wrong game's art: it then keeps its console's mark.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"

	"github.com/openpak/website/internal/titleart"
)

const (
	tileSize    = 256
	jpegQuality = 85
	userAgent   = "openpak-title-art/1 (https://openpak.org)"
	sgdbAPI     = "https://www.steamgriddb.com/api/v2"
)

// inset is how much of a cover is platform banner or border, as fractions of its height
// (t, b) and width (l, r).
type inset struct{ t, b, l, r float64 }

// crops are the banner insets of each console's front covers, measured on the sources
// above: the Wii white band, the DS/3DS white side band, the PS2/PSP/Vita black or blue
// band, the Xbox green frame and band, the Wii U blue band. PS3 covers changed layout
// (a black side band before 2010, a top band after), so its rows say which.
var crops = map[string]inset{
	"Wii":      {t: 0.085},
	"Wii U":    {t: 0.08},
	"DS":       {l: 0.145},
	"3DS":      {t: 0.09},
	"PS2":      {t: 0.07},
	"PSP":      {t: 0.055},
	"PS3":      {t: 0.075},
	"Vita":     {t: 0.10},
	"Xbox":     {t: 0.09, l: 0.025, r: 0.025, b: 0.02},
	"Xbox 360": {t: 0.10},
}

type row struct {
	Console, Name, Source, Author, Crop string
}

func (r row) square() bool {
	return strings.Contains(r.Source, "steamgriddb.com") || strings.HasPrefix(r.Source, "file:")
}

func main() {
	manifest := flag.String("manifest", "scripts/title-art/titles.tsv", "the title → art table")
	out := flag.String("out", "web/static/art/titles", "where the tiles and SOURCES.tsv go")
	cacheDir := flag.String("cache", "", "download cache (default: the user cache dir)")
	sheets := flag.String("sheets", "", "write a contact sheet per console here")
	only := flag.String("only", "", "only this console")
	candidates := flag.Bool("candidates", false, "look titles without a row up on SteamGridDB (needs STEAMGRIDDB_API_KEY)")
	title := flag.String("title", "", "with -candidates: only this title (even if it has a row)")
	term := flag.String("term", "", "with -candidates -title: the name SteamGridDB knows the game by")
	catalog := flag.String("catalog", "https://openpak.org/api/v1/catalog.json", "the catalogue, for -candidates")
	flag.Parse()
	if *cacheDir == "" {
		d, err := os.UserCacheDir()
		if err != nil {
			fail(err)
		}
		*cacheDir = filepath.Join(d, "openpak-title-art")
	}
	if err := os.MkdirAll(*cacheDir, 0o755); err != nil {
		fail(err)
	}
	f := &fetcher{dir: *cacheDir, client: &http.Client{Timeout: 60 * time.Second}}
	rows, err := readManifest(*manifest)
	if err != nil {
		fail(err)
	}
	if *candidates {
		key := os.Getenv("STEAMGRIDDB_API_KEY")
		if key == "" {
			fail(errors.New("-candidates needs STEAMGRIDDB_API_KEY"))
		}
		dir := *sheets
		if dir == "" {
			dir = filepath.Join(*cacheDir, "candidates")
		}
		if err := findCandidates(f, key, *catalog, rows, *only, *title, *term, dir); err != nil {
			fail(err)
		}
		return
	}
	if err := build(f, rows, *out, *only, *sheets); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "title-art:", err)
	os.Exit(1)
}

func readManifest(path string) ([]row, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []row
	seen := map[string]bool{}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		c := strings.Split(line, "\t")
		for len(c) < 5 {
			c = append(c, "")
		}
		r := row{Console: c[0], Name: c[1], Source: c[2], Author: c[3], Crop: c[4]}
		if r.Console == "" || r.Name == "" || r.Source == "" {
			return nil, fmt.Errorf("%s:%d: console, name and source are required", path, i+1)
		}
		if _, ok := crops[r.Console]; !ok {
			return nil, fmt.Errorf("%s:%d: unknown console %q", path, i+1, r.Console)
		}
		if _, err := parseCrop(crops[r.Console], r.Crop); err != nil {
			return nil, fmt.Errorf("%s:%d: %v", path, i+1, err)
		}
		slug := titleart.Slug(r.Console, r.Name)
		if seen[slug] {
			return nil, fmt.Errorf("%s:%d: %s twice", path, i+1, slug)
		}
		seen[slug] = true
		rows = append(rows, r)
	}
	return rows, nil
}

// cropSpec is a cover's insets plus where the square sits in what remains.
type cropSpec struct {
	inset
	x, y, z float64
}

func parseCrop(def inset, s string) (cropSpec, error) {
	c := cropSpec{inset: def, x: 0.5, y: 0, z: 1}
	for _, kv := range strings.Fields(s) {
		k, v, ok := strings.Cut(kv, "=")
		n, err := strconv.ParseFloat(v, 64)
		if !ok || err != nil || n < 0 || n > 1 {
			return c, fmt.Errorf("bad crop %q", kv)
		}
		switch k {
		case "t":
			c.t = n
		case "b":
			c.b = n
		case "l":
			c.l = n
		case "r":
			c.r = n
		case "x":
			c.x = n
		case "y":
			c.y = n
		case "z":
			if n == 0 {
				return c, fmt.Errorf("bad crop %q", kv)
			}
			c.z = n
		default:
			return c, fmt.Errorf("bad crop %q", kv)
		}
	}
	return c, nil
}

// cut is the square of a cover the tile is made from.
func cut(b image.Rectangle, c cropSpec) image.Rectangle {
	w, h := float64(b.Dx()), float64(b.Dy())
	x0, y0 := c.l*w, c.t*h
	aw, ah := w*(1-c.l-c.r), h*(1-c.t-c.b)
	side := min(aw, ah) * c.z
	x := x0 + (aw-side)*c.x
	y := y0 + (ah-side)*c.y
	r := image.Rect(int(x+0.5), int(y+0.5), int(x+side+0.5), int(y+side+0.5)).Add(b.Min)
	return r.Intersect(b)
}

func build(f *fetcher, rows []row, out, only, sheets string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	keep := map[string]bool{"SOURCES.tsv": true}
	var src bytes.Buffer
	src.WriteString("console\tname\tfile\tsource\tauthor\n")
	byConsole := map[string][]labelled{}
	var consoles []string
	failed := 0
	for _, r := range rows {
		file := titleart.Slug(r.Console, r.Name) + ".jpg"
		keep[file] = true
		if only != "" && r.Console != only {
			continue
		}
		img, err := f.image(r.Source)
		if err == nil {
			var tile image.Image
			tile, err = makeTile(img, r)
			if err == nil {
				err = writeJPEG(filepath.Join(out, file), tile)
			}
			if err == nil {
				if _, ok := byConsole[r.Console]; !ok {
					consoles = append(consoles, r.Console)
				}
				byConsole[r.Console] = append(byConsole[r.Console], labelled{tile, r.Name})
			}
		}
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "%s / %s: %v\n", r.Console, r.Name, err)
			continue
		}
		fmt.Fprintf(&src, "%s\t%s\t%s\t%s\t%s\n", r.Console, r.Name, file, r.Source, r.Author)
	}
	if only == "" {
		if err := os.WriteFile(filepath.Join(out, "SOURCES.tsv"), src.Bytes(), 0o644); err != nil {
			return err
		}
		entries, _ := os.ReadDir(out)
		for _, e := range entries {
			if !keep[e.Name()] && strings.HasSuffix(e.Name(), ".jpg") {
				fmt.Fprintln(os.Stderr, "removing", e.Name())
				os.Remove(filepath.Join(out, e.Name()))
			}
		}
	}
	if sheets != "" {
		if err := os.MkdirAll(sheets, 0o755); err != nil {
			return err
		}
		// Full size, twenty to a page, so every edge and corner can be checked.
		const page = 20
		for _, c := range consoles {
			items := byConsole[c]
			for p := 0; p*page < len(items); p++ {
				name := filepath.Join(sheets, fmt.Sprintf("%s-%d.jpg", strings.ReplaceAll(strings.ToLower(c), " ", "-"), p+1))
				if err := writeJPEG(name, contactSheet(items[p*page:min(len(items), (p+1)*page)], 5, tileSize)); err != nil {
					return err
				}
				fmt.Println("sheet", name)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d tiles failed", failed)
	}
	return nil
}

func makeTile(img image.Image, r row) (image.Image, error) {
	b := img.Bounds()
	if r.square() {
		if b.Dx() < tileSize || b.Dx() != b.Dy() {
			return nil, fmt.Errorf("grid is %dx%d, want a square of at least %d", b.Dx(), b.Dy(), tileSize)
		}
		return scale(img, b), nil
	}
	c, _ := parseCrop(crops[r.Console], r.Crop)
	sq := cut(b, c)
	if sq.Dx() < 160 {
		return nil, fmt.Errorf("cover too small (%v → %v)", b, sq)
	}
	return scale(img, sq), nil
}

func scale(img image.Image, from image.Rectangle) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, tileSize, tileSize))
	// A transparent grid lands on black rather than JPEG's undefined background.
	draw.Draw(dst, dst.Bounds(), image.Black, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, from, draw.Over, nil)
	return dst
}

func writeJPEG(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// fetcher downloads once per URL into a cache directory. Everything it fetches is
// untrusted: it is only ever decoded as an image, within size limits.
type fetcher struct {
	dir    string
	client *http.Client
	auth   string
}

const maxBytes = 25 << 20

func (f *fetcher) get(u string, header map[string]string) ([]byte, error) {
	if name, ok := strings.CutPrefix(u, "file:"); ok {
		if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
			return nil, fmt.Errorf("%s: a file source is a bare name in scripts/title-art/art", u)
		}
		b, err := os.ReadFile(filepath.Join("scripts", "title-art", "art", name))
		if err == nil && len(b) > maxBytes {
			return nil, fmt.Errorf("%s: over %d bytes", u, maxBytes)
		}
		return b, err
	}
	sum := sha256.Sum256([]byte(u))
	path := filepath.Join(f.dir, hex.EncodeToString(sum[:12]))
	if b, err := os.ReadFile(path); err == nil {
		return b, nil
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	var resp *http.Response
	for try := 0; ; try++ {
		resp, err = f.client.Do(req)
		if err == nil && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			break
		}
		if err == nil {
			resp.Body.Close()
			err = fmt.Errorf("%s: %s", u, resp.Status)
		}
		if try == 3 {
			return nil, err
		}
		time.Sleep(time.Duration(2<<try) * time.Second)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBytes {
		return nil, fmt.Errorf("%s: over %d bytes", u, maxBytes)
	}
	// API answers are not cached: they change as people upload.
	if !strings.HasPrefix(u, sgdbAPI) {
		if err := os.WriteFile(path, b, 0o644); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (f *fetcher) image(u string) (image.Image, error) {
	b, err := f.get(u, nil)
	if err != nil {
		return nil, err
	}
	b = dropICCP(b)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", u, err)
	}
	if cfg.Width > 6000 || cfg.Height > 6000 {
		return nil, fmt.Errorf("%s: %dx%d is too large", u, cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", u, err)
	}
	return img, nil
}

// dropICCP removes a PNG's colour profile chunk: some libretro covers carry one with a
// bad checksum, which Go's decoder rejects, and a tile has no use for it.
func dropICCP(b []byte) []byte {
	const sig = "\x89PNG\r\n\x1a\n"
	if len(b) < 8 || string(b[:8]) != sig {
		return b
	}
	out := append([]byte(nil), b[:8]...)
	for i := 8; i+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i:]))
		end := i + 12 + n
		if n < 0 || end > len(b) {
			return b
		}
		if string(b[i+4:i+8]) != "iCCP" {
			out = append(out, b[i:end]...)
		}
		i = end
	}
	return out
}

// labelled is a picture with its caption, for contact sheets.
type labelled struct {
	img   image.Image
	label string
}

// contactSheet lays pictures out cols wide at cell px with their captions under them.
func contactSheet(items []labelled, cols, cell int) image.Image {
	const pad, capH = 8, 30
	rows := (len(items) + cols - 1) / cols
	w := cols*(cell+pad) + pad
	h := rows*(cell+capH+pad) + pad
	sheet := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(sheet, sheet.Bounds(), image.NewUniform(color.RGBA{40, 40, 46, 255}), image.Point{}, draw.Src)
	for i, it := range items {
		if it.img == nil {
			continue
		}
		x := pad + (i%cols)*(cell+pad)
		y := pad + (i/cols)*(cell+capH+pad)
		b := it.img.Bounds()
		r := image.Rect(x, y, x+cell, y+cell)
		if b.Dx() != b.Dy() { // keep the aspect of a non-square picture
			if b.Dx() > b.Dy() {
				r.Max.Y = y + cell*b.Dy()/b.Dx()
			} else {
				r.Max.X = x + cell*b.Dx()/b.Dy()
			}
		}
		draw.ApproxBiLinear.Scale(sheet, r, it.img, b, draw.Src, nil)
		d := font.Drawer{Dst: sheet, Src: image.White, Face: basicfont.Face7x13}
		for j, line := range wrap(it.label, cell/7, 2) {
			d.Dot = fixed.P(x, y+cell+12+j*13)
			d.DrawString(line)
		}
	}
	return sheet
}

func wrap(s string, width, lines int) []string {
	var out []string
	for s != "" && len(out) < lines {
		if len(s) <= width {
			out = append(out, s)
			break
		}
		cut := strings.LastIndex(s[:width], " ")
		if cut <= 0 {
			cut = width
		}
		out = append(out, s[:cut])
		s = strings.TrimSpace(s[cut:])
	}
	return out
}

// --- SteamGridDB candidates -------------------------------------------------

type catalogRow struct {
	Console, Name, Kind string
}

type sgdbGame struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	ReleaseDate int64  `json:"release_date"`
}

type sgdbGrid struct {
	ID       int    `json:"id"`
	Score    int    `json:"score"`
	Style    string `json:"style"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Notes    string `json:"notes"`
	Language string `json:"language"`
	URL      string `json:"url"`
	Thumb    string `json:"thumb"`
	Upvotes  int    `json:"upvotes"`
	Author   struct {
		Name string `json:"name"`
	} `json:"author"`
}

func (f *fetcher) sgdb(key, path string, v any) error {
	b, err := f.get(sgdbAPI+path, map[string]string{"Authorization": "Bearer " + key})
	if err != nil {
		return errors.New(strings.ReplaceAll(err.Error(), key, "…"))
	}
	var resp struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("steamgriddb %s: %s", path, b)
	}
	return json.Unmarshal(resp.Data, v)
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// norm compares game names loosely: case, punctuation, "&"/"and", "Pokémon".
func norm(s string) string {
	s = strings.ToLower(strings.Map(func(r rune) rune {
		if r == 'é' || r == 'É' {
			return 'e'
		}
		return r
	}, s))
	s = strings.ReplaceAll(s, "&", " and ")
	s = nonWord.ReplaceAllString(s, " ")
	words := strings.Fields(s)
	keep := words[:0]
	for _, w := range words {
		if w != "and" && w != "the" {
			keep = append(keep, w)
		}
	}
	return strings.Join(keep, " ")
}

// acceptable are the grid styles built from the game's own art with its logo on.
var acceptable = map[string]int{"official": 0, "alternate": 1}

func findCandidates(f *fetcher, key, catalogURL string, rows []row, only, title, term, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := f.get(catalogURL+"?t="+strconv.FormatInt(time.Now().Unix()/3600, 10), nil)
	if err != nil {
		return err
	}
	var cat []catalogRow
	if err := json.Unmarshal(b, &cat); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, r := range rows {
		have[titleart.Slug(r.Console, r.Name)] = true
	}
	tsv, err := os.OpenFile(filepath.Join(dir, "candidates.tsv"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer tsv.Close()
	w := bufio.NewWriter(tsv)
	defer w.Flush()
	if st, err := tsv.Stat(); err == nil && st.Size() == 0 {
		fmt.Fprintln(w, "console\tname\tgame\tgame_name\tyear\tn\tgrid\tstyle\tscore\turl\tauthor\tnotes")
	}
	var sheet []labelled
	n := 0
	prefix := ""
	if title != "" {
		prefix = titleart.Slug(only, title) + "-"
	}
	flush := func() error {
		if len(sheet) == 0 {
			return nil
		}
		n++
		name := filepath.Join(dir, fmt.Sprintf("candidates-%s%02d.jpg", prefix, n))
		fmt.Println("sheet", name)
		err := writeJPEG(name, contactSheet(sheet, 9, 150))
		sheet = nil
		return err
	}
	titles := 0
	for _, c := range cat {
		if c.Kind != "title" || c.Console == "Switch" || (only != "" && c.Console != only) {
			continue
		}
		if _, ok := crops[c.Console]; !ok || (title == "" && have[titleart.Slug(c.Console, c.Name)]) || (title != "" && c.Name != title) {
			continue
		}
		search := searchTerm(c.Name)
		if term != "" {
			search = term
		}
		var games []sgdbGame
		if err := f.sgdb(key, "/search/autocomplete/"+url.PathEscape(search), &games); err != nil {
			fmt.Fprintf(os.Stderr, "%s / %s: %v\n", c.Console, c.Name, err)
			continue
		}
		var match []sgdbGame
		for _, g := range games {
			if norm(g.Name) == norm(search) {
				match = append(match, g)
			}
		}
		if len(match) == 0 {
			var names []string
			for _, g := range games {
				names = append(names, fmt.Sprintf("%s [%d %s]", g.Name, g.ID, year(g.ReleaseDate)))
			}
			fmt.Fprintf(w, "%s\t%s\t\t(no exact match: %s)\n", c.Console, c.Name, strings.Join(names, "; "))
			continue
		}
		titles++
		row := []labelled{}
		for _, g := range match {
			var grids []sgdbGrid
			if err := f.sgdb(key, fmt.Sprintf("/grids/game/%d?dimensions=512x512,1024x1024&nsfw=false&humor=false&epilepsy=false", g.ID), &grids); err != nil {
				fmt.Fprintf(os.Stderr, "%s / %s: %v\n", c.Console, c.Name, err)
				continue
			}
			ok := grids[:0]
			for _, gr := range grids {
				if _, good := acceptable[gr.Style]; good && gr.Width == gr.Height {
					ok = append(ok, gr)
				}
			}
			sort.SliceStable(ok, func(i, j int) bool {
				if acceptable[ok[i].Style] != acceptable[ok[j].Style] {
					return acceptable[ok[i].Style] < acceptable[ok[j].Style]
				}
				return ok[i].Score > ok[j].Score
			})
			for i, gr := range ok {
				if i == 8 {
					break
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%d\t%d\t%s\t%d\t%s\t%s\t%s\n", c.Console, c.Name, g.ID, g.Name, year(g.ReleaseDate),
					len(row), gr.ID, gr.Style, gr.Score, gr.URL, gr.Author.Name, oneLine(gr.Notes))
				thumb := gr.Thumb
				if thumb == "" {
					thumb = gr.URL
				}
				img, err := f.image(thumb)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s / %s: %v\n", c.Console, c.Name, err)
					continue
				}
				row = append(row, labelled{img, fmt.Sprintf("%d %s #%d %s", len(row), year(g.ReleaseDate), gr.ID, c.Name)})
			}
		}
		if len(row) == 0 {
			fmt.Fprintf(w, "%s\t%s\t\t(no acceptable grid)\n", c.Console, c.Name)
			continue
		}
		// One title per sheet row: its caption cell, then its candidates.
		for len(row)%9 != 0 {
			row = append(row, labelled{})
		}
		sheet = append(sheet, row...)
		if titles%8 == 0 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// searchTerm is what SteamGridDB knows a title as: the first of "X / Y" versions.
func searchTerm(name string) string {
	if a, _, ok := strings.Cut(name, " / "); ok {
		return a
	}
	return name
}

func year(unix int64) string {
	if unix == 0 {
		return "?"
	}
	return strconv.Itoa(time.Unix(unix, 0).UTC().Year())
}

func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}
