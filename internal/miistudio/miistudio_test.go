package miistudio

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Vectors come from running PretendoNetwork/mii-js (AGPL-3.0-only) encodeStudio
// on generated Ver3 Miis; the first studio byte is that run's random seed.
func TestStudioDataMatchesMiiJS(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []struct{ Mii, Studio string }
	if err := json.Unmarshal(raw, &vs); err != nil {
		t.Fatal(err)
	}
	if len(vs) < 10 {
		t.Fatalf("only %d vectors", len(vs))
	}
	for i, v := range vs {
		b, _ := hex.DecodeString(v.Mii)
		want, _ := hex.DecodeString(v.Studio)
		m, err := Parse(b)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		got := m.StudioData(want[0])
		if hex.EncodeToString(got) != v.Studio {
			t.Errorf("vector %d:\n got %x\nwant %s", i, got, v.Studio)
		}
		if len(got) != 47 {
			t.Errorf("vector %d: %d bytes", i, len(got))
		}
	}
}

func TestParseRejectsBadData(t *testing.T) {
	for _, s := range []string{"", "AAAA", "not base64 !!"} {
		if _, err := Parse64(s); err == nil {
			t.Errorf("Parse64(%q) accepted", s)
		}
	}
	if _, err := Parse(make([]byte, StoreDataSize)); err == nil {
		t.Error("all-zero Mii accepted (eyebrow y 0 is out of range)")
	}
}

func TestURLShape(t *testing.T) {
	raw, _ := os.ReadFile("testdata/vectors.json")
	var vs []struct{ Mii string }
	_ = json.Unmarshal(raw, &vs)
	b, _ := hex.DecodeString(vs[0].Mii)
	m, _ := Parse(b)
	u := URL(m, Expression("smile_open_mouth.png"))
	for _, want := range []string{
		"https://studio.mii.nintendo.com/miis/image.png?", "type=face", "expression=smile_open_mouth",
		"width=270", "bgColor=FFFFFF00", "data=",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("%s missing %q", u, want)
		}
	}
	if u != URL(m, "smile_open_mouth") {
		t.Error("URL not stable")
	}
}

func TestExpression(t *testing.T) {
	for in, want := range map[string]string{
		"normal_face.png": "normal", "standard.tga": "normal", "smile_open_mouth": "smile_open_mouth",
		"wink_left.png": "wink_left", "frustrated": "frustrated", "bogus": "normal", "": "normal",
	} {
		if got := Expression(in); got != want {
			t.Errorf("Expression(%q)=%q want %q", in, got, want)
		}
	}
}
