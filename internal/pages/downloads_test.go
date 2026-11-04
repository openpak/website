package pages

import (
	"context"
	"github.com/openpak/website/internal/store"
	"os"
	"strings"
	"testing"
)

// Downloads read by family and console, oldest first; a console with nothing to download is left
// out, and a client with no published release is not offered.
func TestDownloadsByFamilyAndGeneration(t *testing.T) {
	s, _, _ := newTestServer(t)
	unreleased := []string{"azahar", "melonds", "berth", "wharf", "pier", "ds-patcher"}
	s.ReleaseCheck = func(_ context.Context, url string) bool {
		for _, r := range unreleased {
			if strings.Contains(url, "/"+r+"/") {
				return false
			}
		}
		return true
	}
	body := get(t, s, "/downloads").Body.String()
	order := []string{`id="fam-nintendo"`, ">Wii</h3>", ">Wii U</h3>", ">3DS</h3>", ">Switch</h3>", `id="fam-openpak"`}
	last := -1
	for _, want := range order {
		i := strings.Index(body, want)
		if i < 0 || i < last {
			t.Fatalf("%s missing or out of order (at %d after %d)", want, i, last)
		}
		last = i
	}
	for _, gone := range []string{`id="fam-sony"`, `id="fam-microsoft"`, ">DS</h3>", "Azahar", "melonDS", "Wharf"} {
		if strings.Contains(body, gone) {
			t.Errorf("%q should be left out: nothing to download", gone)
		}
	}
	for _, link := range []string{"harbor/releases/latest", "quay/releases/latest", "marina/releases/latest", "switch-patcher/releases/latest", "openpak/app/releases/latest"} {
		if !strings.Contains(body, link) {
			t.Errorf("link %s missing", link)
		}
	}
}

func TestDownloadsWithoutAReleaseCheckOfferEverything(t *testing.T) {
	s, _, _ := newTestServer(t)
	body := get(t, s, "/downloads").Body.String()
	for _, want := range []string{"Azahar", "melonDS", `id="fam-microsoft"`, `id="fam-sony"`, "Original Xbox"} {
		if !strings.Contains(body, want) {
			t.Errorf("%q missing", want)
		}
	}
}

// TestWriteDownloadsPreview renders /downloads as it stands today (clients without a release left
// out) for a human to look at. Runs only when PREVIEW_DIR is set.
func TestWriteDownloadsPreview(t *testing.T) {
	dir := os.Getenv("PREVIEW_DIR")
	if dir == "" {
		t.Skip("set PREVIEW_DIR")
	}
	t.Chdir("../..")
	s, _, db := newTestServer(t)
	var feats []store.Request
	for c, st := range map[string]string{"Switch": "live", "Wii U": "beta", "3DS": "beta", "Wii": "beta", "DS": "beta", "Xbox 360": "live", "Xbox": "in-development", "PS3": "in-development"} {
		feats = append(feats, store.Request{Kind: "feature", Name: c + " online", Console: c, Status: st})
	}
	_ = db.SeedRequests(context.Background(), feats)
	s.ReleaseCheck = func(_ context.Context, url string) bool {
		for _, r := range []string{"azahar", "melonds", "berth", "wharf", "pier", "ds-patcher"} {
			if strings.Contains(url, "/"+r+"/") {
				return false
			}
		}
		return true
	}
	write(t, dir, "downloads", get(t, s, "/downloads").Body.String())
}
