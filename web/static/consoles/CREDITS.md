# Console marks

One mark per console, normalised into a 36x24 slot. The slot is 3:2 because most
of these are wordmarks and a square box shrinks a 6:1 logotype to an illegible
smear; square device marks letterbox inside it. The app ships byte-for-byte
copies in `app/assets/consoles`.

Every console mark is the real logo. Only `generic.svg`, the fallback for a
console with no mark, is drawn here.

| File | Source | Licence | Colour |
|---|---|---|---|
| `switch.svg` | `cib:nintendo-switch` | CoreUI Brands, CC0 1.0 | tinted `#E60012` (the mark is monochrome; this is Nintendo's own red) |
| `wii.svg` | `cib:wii` | CoreUI Brands, CC0 1.0 | tinted — no colour version exists |
| `wii-u.svg` | `cib:wiiu` | CoreUI Brands, CC0 1.0 | tinted — no colour version exists |
| `3ds.svg` | `cib:nintendo-3ds` | CoreUI Brands, CC0 1.0 | tinted — no colour version exists |
| `ds.svg` | Commons, `Nintendo DS Logo.svg` | public domain (text logo) | **own colours** |
| `xbox.svg` | `thesvg-color:xbox` | theSVG, MIT | **own colours** |
| `xbox-360.svg` | Commons, `X Box 360 logo.svg` | public domain (text logo) | **own colours** |
| `ps2.svg` | `thesvg-color:playstation-2` | theSVG, MIT | **own colours** |
| `ps3.svg` | `thesvg-color:playstation-3` | theSVG, MIT | **own colours** |
| `psp.svg` | `thesvg-color:playstation-portable` | theSVG, MIT | **own colours** |
| `vita.svg` | `thesvg-color:playstation-vita` | theSVG, MIT | **own colours** |
| `generic.svg` | drawn here | — | ours |

## Why every mark ships twice

A brand colour is chosen for print and for the company's own white page, not for
our dark row. Against `#1c1e24` the PlayStation family's `#003791` scores 1.6:1
and the DS logo's `#221E1F` scores 1.0:1 — invisible. So each mark also has a
`-dark.svg`: same art, with any colour below 3:1 lifted in lightness with its hue
kept (a near-neutral goes grey rather than brown, and is lifted to 4.5:1 because
grey needs more room to read). Marks that already pass — Switch red, Xbox green,
the Xbox 360 lockup, and every tinted one — have an identical `-dark.svg`, which
keeps both clients dumb: they append `-dark` and never ask whether it differs.

The website picks with `<picture><source media="(prefers-color-scheme: dark)">`,
no CSS and no JS; the app passes `Theme.of(context).brightness` to
`consoleAsset`. A browser that reports no preference gets the light file.

theSVG: MIT License, Copyright (c) 2025 thesvg.org —
https://github.com/glincker/thesvg/blob/main/LICENSE

CoreUI Brands is CC0 and the Commons files are public domain as text logos, so
none require attribution; this table is provenance, not obligation. The marks
remain the trademarks of their owners, who are not affiliated with OpenPak and do
not endorse it.

## Regenerating

Fetched from the Iconify API or Commons, then flattened: metadata, unreferenced
gradients and namespaced editor attributes stripped (they would be unbound
prefixes once the body is rewrapped), and the art scaled to the box — wordmarks
to its width, device marks to whichever side binds first. Keep `consoleSlug` (Go)
and `consoleAsset` (Dart) in step with the filenames here.
