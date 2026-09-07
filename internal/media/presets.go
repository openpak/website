package media

import (
	"image"
	"image/color"
	"math"
)

// Preset avatars are drawn here rather than shipped as files: no binary
// assets in the repo, nothing copyrighted, and a new one is a palette entry.
// Each is a coloured field with an off-centre disc and a smaller companion.

// PresetIDs is the offered set, in display order.
var PresetIDs = []string{
	"aurora", "ember", "moss", "cobalt", "plum", "sand",
	"teal", "rose", "slate", "lime", "indigo", "clay",
}

var presetColors = map[string][2]color.RGBA{
	// {field, mark}
	"aurora": {{0x1f, 0x3a, 0x5f, 0xff}, {0x7e, 0xc8, 0xe3, 0xff}},
	"ember":  {{0x5a, 0x1e, 0x1e, 0xff}, {0xff, 0x9f, 0x5b, 0xff}},
	"moss":   {{0x24, 0x3d, 0x28, 0xff}, {0x9c, 0xd1, 0x7f, 0xff}},
	"cobalt": {{0x17, 0x24, 0x5c, 0xff}, {0x86, 0x9d, 0xff, 0xff}},
	"plum":   {{0x3d, 0x1f, 0x47, 0xff}, {0xd8, 0x9c, 0xe8, 0xff}},
	"sand":   {{0x54, 0x45, 0x28, 0xff}, {0xf0, 0xd2, 0x8c, 0xff}},
	"teal":   {{0x11, 0x3f, 0x3f, 0xff}, {0x6d, 0xd8, 0xc8, 0xff}},
	"rose":   {{0x55, 0x1f, 0x33, 0xff}, {0xff, 0x9d, 0xb8, 0xff}},
	"slate":  {{0x2b, 0x30, 0x38, 0xff}, {0xb4, 0xc0, 0xd0, 0xff}},
	"lime":   {{0x33, 0x44, 0x14, 0xff}, {0xc8, 0xe6, 0x6b, 0xff}},
	"indigo": {{0x25, 0x1d, 0x52, 0xff}, {0xa9, 0x96, 0xff, 0xff}},
	"clay":   {{0x4a, 0x2e, 0x22, 0xff}, {0xe0, 0xa1, 0x7c, 0xff}},
}

// IsPreset reports whether id names a built-in avatar.
func IsPreset(id string) bool {
	_, ok := presetColors[id]
	return ok
}

// drawPreset renders one preset: a mirrored block pattern on a tinted
// field. Deterministic from the name, so the same id always draws the same
// mark, and symmetric so it reads as designed rather than as noise.
func drawPreset(id string, size int) image.Image {
	c, ok := presetColors[id]
	if !ok {
		c = presetColors["slate"]
	}
	field, mark := c[0], c[1]
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Field first.
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, field)
		}
	}

	// A 5x5 grid, left three columns decided by the hash and the right two
	// mirrored, which is what makes it look deliberate.
	const cells = 5
	grid := patternFor(id)
	pad := float64(size) * 0.14
	cell := (float64(size) - 2*pad) / cells
	for row := 0; row < cells; row++ {
		for col := 0; col < cells; col++ {
			if !grid[row][col] {
				continue
			}
			x0 := int(pad + float64(col)*cell)
			y0 := int(pad + float64(row)*cell)
			x1 := int(pad + float64(col+1)*cell)
			y1 := int(pad + float64(row+1)*cell)
			for y := y0; y < y1 && y < size; y++ {
				for x := x0; x < x1 && x < size; x++ {
					img.SetRGBA(x, y, mark)
				}
			}
		}
	}
	return img
}

// patternFor derives the mirrored 5x5 grid from the preset name. FNV-1a is
// enough: this needs to be stable, not unpredictable.
func patternFor(id string) [5][5]bool {
	var h uint32 = 2166136261
	for i := 0; i < len(id); i++ {
		h ^= uint32(id[i])
		h *= 16777619
	}
	next := func() uint32 { // xorshift, stable across builds
		h ^= h << 13
		h ^= h >> 17
		h ^= h << 5
		return h
	}
	// Reroll until the mark has a sensible weight: too few cells looks
	// broken, too many looks like a solid block.
	for attempt := 0; ; attempt++ {
		var g [5][5]bool
		filled := 0
		for row := 0; row < 5; row++ {
			for col := 0; col < 3; col++ {
				on := next()&1 == 1
				g[row][col] = on
				g[row][4-col] = on
				if on {
					filled++
					if col != 2 { // the centre column is not mirrored
						filled++
					}
				}
			}
		}
		if (filled >= 9 && filled <= 17) || attempt >= 16 {
			return g
		}
	}
}

// coverage is 1 inside the circle, 0 outside, ramped across one pixel.
func coverage(px, py, cx, cy, r float64) float64 {
	d := math.Hypot(px-cx, py-cy)
	switch {
	case d <= r-0.5:
		return 1
	case d >= r+0.5:
		return 0
	default:
		return r + 0.5 - d
	}
}

func blend(a, b color.RGBA, t float64) color.RGBA {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.RGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 0xff}
}
