# Console marks

One SVG per console, recoloured to that console's palette entry and normalised
into a 36x24 slot. The slot is 3:2 because most of these marks are wordmarks and
a square box shrinks a 6:1 logotype to an illegible smear; square device marks
letterbox inside it. The app ships byte-for-byte copies in `app/assets/consoles`.

Every console mark is the real thing. Only `generic.svg`, the fallback for a
console with no mark, is drawn here.

| File | Source | Licence |
|---|---|---|
| `switch.svg` | `cib:nintendo-switch` | CoreUI Brands, CC0 1.0 |
| `wii.svg` | `cib:wii` | CoreUI Brands, CC0 1.0 |
| `wii-u.svg` | `cib:wiiu` | CoreUI Brands, CC0 1.0 |
| `3ds.svg` | `cib:nintendo-3ds` | CoreUI Brands, CC0 1.0 |
| `xbox.svg` | `cib:xbox` | CoreUI Brands, CC0 1.0 |
| `ps3.svg` | `cib:playstation3` | CoreUI Brands, CC0 1.0 |
| `ps2.svg` | `thesvg:playstation-2` | theSVG, MIT |
| `psp.svg` | `thesvg:playstation-portable` | theSVG, MIT |
| `vita.svg` | `thesvg:playstation-vita` | theSVG, MIT |
| `ds.svg` | Wikimedia Commons, `Nintendo DS Logo.svg` | public domain (below the threshold of originality) |
| `xbox-360.svg` | Wikimedia Commons, `X Box 360 logo.svg` | public domain (below the threshold of originality) |
| `generic.svg` | drawn here | — |

theSVG: MIT License, Copyright (c) 2025 thesvg.org —
https://github.com/glincker/thesvg/blob/main/LICENSE

CoreUI Brands is CC0 (public domain dedication) and the two Commons files are
public domain as text logos, so none of them require attribution; this table is
provenance, not obligation. The marks remain the trademarks of their owners, who
are not affiliated with OpenPak and do not endorse it.

## Regenerating

Marks are fetched from the Iconify API or Commons, then flattened: gradients and
namespaced editor attributes stripped (they would be unbound prefixes once the
body is rewrapped), all fills replaced by a single colour on the wrapper, and the
art scaled to the box — wordmarks to its width, device marks to whichever side
binds first. Keep `consoleSlug` (Go) and `consoleAsset` (Dart) in step with the
filenames here.
