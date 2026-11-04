// Package miistudio turns an account's Mii (Wii U / 3DS FFLStoreData, "Ver3",
// 96 bytes, from the account core) into a link to Nintendo's public Mii Studio
// renderer. Copied from nn-account's package of the same name.
//
// OpenPak only links to Studio. It never fetches, stores or caches the
// rendered images; the browser or console follows the link itself.
//
// The Ver3 field layout and the 46-byte "studio" obfuscation are ported from
// PretendoNetwork/mii-js (src/mii.ts: decode, validate and encodeStudio),
// AGPL-3.0-only, the same licence as this module. The test vectors in
// testdata/vectors.json were produced by running that library's own
// encodeStudio on generated Miis.
package miistudio

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// StoreDataSize is the size of a Wii U / 3DS Ver3 store-data Mii.
const StoreDataSize = 0x60

const renderBase = "https://studio.mii.nintendo.com/miis/image.png"

// Mii holds the fields Studio needs plus the favourite colour and name used
// by the fallback avatar.
type Mii struct {
	Gender, FavoriteColor, Build, Height                               int
	FaceType, SkinColor, WrinklesType, MakeupType                      int
	HairType, HairColor, EyeType, EyeColor, EyeScale, EyeVStretch      int
	EyeRotation, EyeSpacing, EyeY                                      int
	EyebrowType, EyebrowColor, EyebrowScale, EyebrowVStretch           int
	EyebrowRotation, EyebrowSpacing, EyebrowY                          int
	NoseType, NoseScale, NoseY                                         int
	MouthType, MouthColor, MouthScale, MouthHStretch, MouthY           int
	MustacheType, BeardType, FacialHairColor, MustacheScale, MustacheY int
	GlassesType, GlassesColor, GlassesScale, GlassesY                  int
	MoleScale, MoleX, MoleY                                            int
	FlipHair, MoleEnabled                                              bool
}

// bits reads LSB-first, the order mii-js's bit stream uses for these fields.
type bits struct {
	b   []byte
	pos int
}

func (r *bits) n(count int) int {
	v := 0
	for i := 0; i < count; i++ {
		v |= int(r.b[r.pos>>3]>>(r.pos&7)&1) << i
		r.pos++
	}
	return v
}
func (r *bits) flag() bool     { return r.n(1) == 1 }
func (r *bits) align()         { r.pos = (r.pos + 7) &^ 7 }
func (r *bits) skip(count int) { r.pos += count }

// Parse decodes and range-checks a Ver3 Mii. The CRC is not enforced: the
// console's own copy is authoritative and a stale checksum must not hide a
// renderable Mii.
func Parse(b []byte) (*Mii, error) {
	if len(b) != StoreDataSize {
		return nil, fmt.Errorf("mii: got %d bytes, want %d", len(b), StoreDataSize)
	}
	if b[0] != 0 && b[0] != 3 {
		return nil, fmt.Errorf("mii: unsupported version %d", b[0])
	}
	r := &bits{b: b}
	m := &Mii{}
	r.skip(8)         // version
	r.skip(2 + 2 + 2) // allow copying, profanity, region lock, character set
	r.align()
	r.skip(8 + 8) // page/slot, unknown + device origin
	r.align()
	r.skip(8 * 8) // system id
	r.skip(4 * 8) // Mii ID block (big endian, not needed)
	r.skip(6 * 8) // console MAC
	r.skip(16)    // padding
	m.Gender = r.n(1)
	r.skip(4 + 5) // birth month, day
	m.FavoriteColor = r.n(4)
	r.skip(1) // favourite flag
	r.align()
	r.skip(20 * 8) // name
	m.Height = r.n(8)
	m.Build = r.n(8)
	r.skip(1) // disable sharing
	m.FaceType = r.n(4)
	m.SkinColor = r.n(3)
	m.WrinklesType = r.n(4)
	m.MakeupType = r.n(4)
	m.HairType = r.n(8)
	m.HairColor = r.n(3)
	m.FlipHair = r.flag()
	r.align()
	m.EyeType = r.n(6)
	m.EyeColor = r.n(3)
	m.EyeScale = r.n(4)
	m.EyeVStretch = r.n(3)
	m.EyeRotation = r.n(5)
	m.EyeSpacing = r.n(4)
	m.EyeY = r.n(5)
	r.align()
	m.EyebrowType = r.n(5)
	m.EyebrowColor = r.n(3)
	m.EyebrowScale = r.n(4)
	m.EyebrowVStretch = r.n(3)
	r.skip(1)
	m.EyebrowRotation = r.n(4)
	r.skip(1)
	m.EyebrowSpacing = r.n(4)
	m.EyebrowY = r.n(5)
	r.align()
	m.NoseType = r.n(5)
	m.NoseScale = r.n(4)
	m.NoseY = r.n(5)
	r.align()
	m.MouthType = r.n(6)
	m.MouthColor = r.n(3)
	m.MouthScale = r.n(4)
	m.MouthHStretch = r.n(3)
	m.MouthY = r.n(5)
	m.MustacheType = r.n(3)
	r.skip(8) // unknown
	m.BeardType = r.n(3)
	m.FacialHairColor = r.n(3)
	m.MustacheScale = r.n(4)
	m.MustacheY = r.n(5)
	r.align()
	m.GlassesType = r.n(4)
	m.GlassesColor = r.n(3)
	m.GlassesScale = r.n(4)
	m.GlassesY = r.n(5)
	m.MoleEnabled = r.flag()
	m.MoleScale = r.n(4)
	m.MoleX = r.n(5)
	m.MoleY = r.n(5)
	if err := m.validate(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Mii) validate() error {
	for _, c := range []struct {
		name     string
		v, limit int // v must be < limit
	}{
		{"gender", m.Gender, 2}, {"favorite color", m.FavoriteColor, 12},
		{"height", m.Height, 128}, {"build", m.Build, 128},
		{"face type", m.FaceType, 12}, {"skin color", m.SkinColor, 7},
		{"wrinkles", m.WrinklesType, 12}, {"makeup", m.MakeupType, 12},
		{"hair type", m.HairType, 132}, {"hair color", m.HairColor, 8},
		{"eye type", m.EyeType, 60}, {"eye color", m.EyeColor, 6}, {"eye scale", m.EyeScale, 8},
		{"eye stretch", m.EyeVStretch, 7}, {"eye rotation", m.EyeRotation, 8},
		{"eye spacing", m.EyeSpacing, 13}, {"eye y", m.EyeY, 19},
		{"eyebrow type", m.EyebrowType, 25}, {"eyebrow color", m.EyebrowColor, 8},
		{"eyebrow scale", m.EyebrowScale, 9}, {"eyebrow stretch", m.EyebrowVStretch, 7},
		{"eyebrow rotation", m.EyebrowRotation, 12}, {"eyebrow spacing", m.EyebrowSpacing, 13},
		{"eyebrow y", m.EyebrowY, 19},
		{"nose type", m.NoseType, 18}, {"nose scale", m.NoseScale, 9}, {"nose y", m.NoseY, 19},
		{"mouth type", m.MouthType, 36}, {"mouth color", m.MouthColor, 5}, {"mouth scale", m.MouthScale, 9},
		{"mouth stretch", m.MouthHStretch, 7}, {"mouth y", m.MouthY, 19},
		{"mustache type", m.MustacheType, 6}, {"beard type", m.BeardType, 6},
		{"facial hair color", m.FacialHairColor, 8}, {"mustache scale", m.MustacheScale, 9},
		{"mustache y", m.MustacheY, 17},
		{"glasses type", m.GlassesType, 9}, {"glasses color", m.GlassesColor, 6},
		{"glasses scale", m.GlassesScale, 8}, {"glasses y", m.GlassesY, 21},
		{"mole scale", m.MoleScale, 9}, {"mole x", m.MoleX, 17}, {"mole y", m.MoleY, 31},
	} {
		if c.v >= c.limit {
			return fmt.Errorf("mii: %s %d out of range", c.name, c.v)
		}
	}
	if m.EyebrowY < 3 {
		return errors.New("mii: eyebrow y out of range")
	}
	return nil
}

// StudioData returns the 46-byte Studio encoding (hex via StudioHex). The
// first byte is the obfuscation seed; every later byte is
// (7 + (value XOR previous byte)) mod 256. mii-js picks a random seed; a
// fixed one keeps the URL stable for a given Mii, so it can be cached by the
// client.
func (m *Mii) StudioData(seed byte) []byte {
	out := []byte{seed}
	prev := seed
	put := func(v int) {
		e := byte(7 + (v^int(prev))&0xff)
		out = append(out, e)
		prev = e
	}
	orEight := func(v int) {
		if v == 0 {
			v = 8
		}
		put(v)
	}
	orEight(m.FacialHairColor)
	put(m.BeardType)
	put(m.Build)
	put(m.EyeVStretch)
	put(m.EyeColor + 8)
	put(m.EyeRotation)
	put(m.EyeScale)
	put(m.EyeType)
	put(m.EyeSpacing)
	put(m.EyeY)
	put(m.EyebrowVStretch)
	orEight(m.EyebrowColor)
	put(m.EyebrowRotation)
	put(m.EyebrowScale)
	put(m.EyebrowType)
	put(m.EyebrowSpacing)
	put(m.EyebrowY)
	put(m.SkinColor)
	put(m.MakeupType)
	put(m.FaceType)
	put(m.WrinklesType)
	put(m.FavoriteColor)
	put(m.Gender)
	switch {
	case m.GlassesColor == 0:
		put(8)
	case m.GlassesColor < 6:
		put(m.GlassesColor + 13)
	default:
		put(0)
	}
	put(m.GlassesScale)
	put(m.GlassesType)
	put(m.GlassesY)
	orEight(m.HairColor)
	put(b2i(m.FlipHair))
	put(m.HairType)
	put(m.Height)
	put(m.MoleScale)
	put(b2i(m.MoleEnabled))
	put(m.MoleX)
	put(m.MoleY)
	put(m.MouthHStretch)
	if m.MouthColor < 4 {
		put(m.MouthColor + 19)
	} else {
		put(0)
	}
	put(m.MouthScale)
	put(m.MouthType)
	put(m.MouthY)
	put(m.MustacheScale)
	put(m.MustacheType)
	put(m.MustacheY)
	put(m.NoseScale)
	put(m.NoseType)
	put(m.NoseY)
	return out
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// DecodeBase64 accepts the base64 forms the console sends (standard or URL
// alphabet, padded or not).
func DecodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("mii: not base64")
}

// Parse64 decodes stored mii_data (base64 Ver3) into a Mii.
func Parse64(s string) (*Mii, error) {
	b, err := DecodeBase64(s)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// expressions are the names Studio accepts.
var expressions = map[string]bool{
	"normal": true, "smile": true, "anger": true, "sorrow": true, "surprise": true, "blink": true,
	"normal_open_mouth": true, "smile_open_mouth": true, "anger_open_mouth": true,
	"surprise_open_mouth": true, "sorrow_open_mouth": true, "blink_open_mouth": true,
	"wink_left": true, "wink_right": true, "wink_left_open_mouth": true, "wink_right_open_mouth": true,
	"like_wink_left": true, "like_wink_right": true, "frustrated": true,
}

// Expression maps an image name used by the consoles and Juxtaposition
// ("normal_face", "smile_open_mouth.png", "standard.tga", ...) to a Studio
// expression, defaulting to "normal".
func Expression(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSuffix(name, "_face")
	if expressions[name] {
		return name
	}
	return "normal"
}

// URL builds the Studio link for a Mii: a transparent face render, 270 px
// wide, for the given expression.
func URL(m *Mii, expression string) string {
	if !expressions[expression] {
		expression = "normal"
	}
	q := url.Values{}
	q.Set("data", fmt.Sprintf("%x", m.StudioData(0)))
	q.Set("type", "face")
	q.Set("expression", expression)
	q.Set("width", "270")
	q.Set("bgColor", "FFFFFF00")
	return renderBase + "?" + q.Encode()
}
