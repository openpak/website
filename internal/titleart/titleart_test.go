package titleart

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlug(t *testing.T) {
	for _, c := range []struct{ console, name, want string }{
		{"Wii U", "Mario Kart 8", "wii-u-mario-kart-8"},
		{"Xbox 360", "Halo: Reach", "xbox-360-halo-reach"},
		{"3DS", "Pokemon X / Y", "3ds-pokemon-x-y"},
		{"DS", "Tony Hawk's American Sk8land", "ds-tony-hawks-american-sk8land"},
		{"3DS", "Code Name: S.T.E.A.M.", "3ds-code-name-steam"},
		{"PS3", "Guilty Gear Xrd -SIGN-", "ps3-guilty-gear-xrd-sign"},
		{"Wii", "Pokémon Battle Revolution", "wii-pokemon-battle-revolution"},
		{"DS", "Warhammer 40,000: Squad Command", "ds-warhammer-40-000-squad-command"},
	} {
		if got := Slug(c.console, c.name); got != c.want {
			t.Errorf("Slug(%q, %q) = %q, want %q", c.console, c.name, got, c.want)
		}
	}
}

func TestSetPath(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"wii-mario-kart-wii.jpg", "notes.txt", "switch-splatoon-3.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := Load(dir)
	if got := s.Path("Wii", "Mario Kart Wii"); got != "/static/art/titles/wii-mario-kart-wii.jpg" {
		t.Errorf("Wii tile: %q", got)
	}
	if got := s.Path("Wii", "Wii Chess"); got != "" {
		t.Errorf("missing tile: %q", got)
	}
	if got := s.Path("Switch", "Splatoon 3"); got != "" {
		t.Errorf("Switch draws its own icon, got %q", got)
	}
	if len(Load(filepath.Join(dir, "nope"))) != 0 {
		t.Error("missing dir is not empty")
	}
}
