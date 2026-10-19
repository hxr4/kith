# Kith brand

The mark is two offset half-rings, the two machines, passing a dot, the
task, between them.

| File | Use |
|---|---|
| `kith-mark.svg` | Default: ink halves, teal dot |
| `kith-mark-small.svg` | 32 px and below (favicons, trays, menu bar) |
| `kith-mark-black.svg`, `kith-mark-white.svg` | One colour |
| `kith-app-icon.svg` | App icon: teal tile, white mark |
| `kith-lockup.svg`, `kith-lockup-dark.svg` | Mark and wordmark, on light and dark |
| `kith-wordmark.svg` | Wordmark alone |
| `geometry.json` | The construction; the app icons are drawn from it |

## Colour

| | HEX | RGB |
|---|---|---|
| Ink | `#13181B` | 19 24 27 |
| Teal | `#0F8C8C` | 15 140 140 |
| Teal on dark | `#5CCFCF` | 92 207 207 |

The dot is the only teal in the two-colour mark. On dark backgrounds the
halves go white and the dot uses Teal on dark. In one colour, the halves
and the dot are the same colour.

## Construction

In a 256 box centred at (128, 128), with scale `s`: the left half-ring is
centred at `128 − (dx, dy)·s` and runs from 90° to 270°; the right is
centred at `128 + (dx, dy)·s` and runs from 270° to 90°. Both have radius
`R·s`, band width `W·s` and round ends. The dot has radius `rd·s` at the
centre.

|  | dx | dy | R | W | rd | s |
|---|---|---|---|---|---|---|
| Main | 34 | 20 | 54 | 36 | 20 | 0.97 |
| Small cut | 40 | 16 | 50 | 44 | 21 | 0.9 |

## Use

- **Clear space:** at least the dot's diameter on every side.
- **Minimum size:** 24 px for the main mark, 12 px for the small cut.
- **Don't:**
  - rotate or mirror the mark (the hand-off reads upper left to lower right)
  - recolour the halves teal
  - add a link line between the halves
  - set the wordmark in another typeface

## Regenerating the app icons

```bash
cd gui && go run ./build/icongen
```

This writes `build/appicon.png`, `build/windows/icon.ico`,
`build/darwin/icons.icns` (on macOS), `icons/tray*.png` and
`frontend/public/icon.png`. The Mac app bundles the same `.icns`.

The macOS app icon is a Liquid Glass icon, `mac/AppIcon.icon`. Edit it in
Icon Composer (Xcode › Open Developer Tool). It has two layers, the halves
and the dot, and a Dark variant: white halves with a light-teal dot.
`mac/scripts/bundle.sh` compiles it with actool.
