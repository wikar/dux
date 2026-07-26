# Dashboard layout

Layout is absolute pixels on a fixed canvas that scales to fit the viewer.
Nothing reflows, so alignment is arithmetic: every edge you want to line up
has to be computed, not eyeballed.

**Plan the whole layout before writing the first element.** Decide the canvas
size, the gap, the slicer column and the visualization areas up front, then
compute each element's `x/y/w/h` from that plan. Retrofitting a grid onto
elements that were placed one at a time never comes out aligned.

[assets/example-dashboard.json](../assets/example-dashboard.json) is a worked
instance of everything below: FHD canvas, `G = 8`, a left rail of five
slicers, and row bands whose edges land on the rail's.

## Canvas sizes

Default to **1920×1080** (FHD) — it matches fullscreen (`?fullscreen`) exactly.

| Target | Fullscreen | View mode (−170px chrome) |
|---|---|---|
| FHD | 1920×1080 | 1920×910 |
| 2K | 2560×1440 | 2560×1270 |
| 4K | 3840×2160 | 3840×1990 |

The view-mode heights deduct 170px for browser chrome plus the DUX navbar.
Pick the fullscreen size for a wall display, the view-mode size for a
dashboard people open in a normal browser tab.

Portrait uses the same numbers with the axes swapped, and the 170px deduction
still comes off the height — which is now the long side:

| Target | Fullscreen | View mode (−170px chrome) |
|---|---|---|
| FHD | 1080×1920 | 1080×1750 |
| 2K | 1440×2560 | 1440×2390 |
| 4K | 2160×3840 | 2160×3670 |

## The gap

Pick **one** gap `G` — a multiple of 8, matching the UI's snap grid — and use
it everywhere: between elements, between areas, and as the outer margin.
**`G = 8` is the default**; anything larger reads as wasted space at FHD.

**Never place an element against a canvas edge.** The first element starts at
`x = G`, `y = G`; the last one ends at `width - G`, `height - G`.

Usable area is therefore `width - 2G` by `height - 2G`.

## Splitting an area

Given an area of width `W` holding `n` elements side by side, each element is
`(W - (n-1)·G) / n` wide. Round to whole pixels and give the remainder to the
last element so the right edges still line up:

```
w      = Math.floor((W - (n - 1) * G) / n)
last_w = W - (n - 1) * (w + G)          // absorbs the rounding remainder
x_i    = area_x + i * (w + G)
```

The same formula applies vertically with `H` and `h`.

Worked example — a 316px-wide bar chart with three KPI cards beneath it at
`G = 8`: each card is `(316 - 2·8) / 3 = 100`, laid out
`100 – 8 – 100 – 8 – 100 = 316`. The cards' outer edges line up with the
chart's.

## No avoidable scrollbars

A scrollbar on an element that *could* have been sized to its content is a
layout bug. Size to the content whenever the content is bounded:

- **Slicers** — a `buttons` slicer must be tall enough for all its pills. If
  it isn't, either give it the height or switch it to `dropdown`. This
  overrides the cardinality table below: a column under 20 values that still
  won't fit becomes a dropdown.
- **Text** — give the markdown the height it renders at.
- **Tables with a known row count** — a `topN: 10` table has exactly ten
  rows; size for them.

Scrolling is correct, and expected, where the content is genuinely unbounded:
an uncapped table, a pivot with many row groups, or a long prose block.

Content height is not something to guess. Publish, then measure the rendered
element — `scrollHeight` vs `clientHeight` on the element body — and adjust
the layout until the avoidable ones are gone.

**A one-pixel overflow still draws a scrollbar.** Measure with
`scrollHeight > clientHeight`, not with a tolerance — an exact fit is not a
fit. Size to `ceil((chrome + content) / 8) * 8`.

A titled element's chrome is a fixed **26px** title bar. A buttons slicer
adds 12px of body padding and a 4px footer strip on top of its pill rows at
20px each, so the heights land on the grid:

| Pill rows | Content | + 42px chrome | Height |
|---|---|---|---|
| 1 | 20 | 62 | **64** |
| 2 | 44 | 86 | **88** |
| 3 | 68 | 110 | **112** |
| n | 24n − 4 | 24n + 38 | `ceil((24n + 38) / 8) * 8` |

Pill rows are not the value count: pills wrap, so short values share a row
and long ones don't. Measure rather than divide.

## Landscape structure

```
┌─ G ─────────────────────────────────────────────────┐
│  ┌─────────┐  ┌──────────────────────────────────┐  │
│  │ slicers │  │                                  │  │
│  │  ▼ G    │  │      visualization area          │  │
│  │ slicers │  │                                  │  │
│  └─────────┘  └──────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

**Slicer column** — one virtual column down the left edge. Slicers stack top
to bottom separated by `G`. If they don't all fit, start a **second column**
to its right, `G` away, and keep filling top to bottom. The visualization
area begins `G` to the right of the last slicer column.

**Logo and hero.** The logo goes at the **top of the slicer column** — top
left of the canvas. The hero (title + subtitle text) may sit directly beneath
it in the same column, or as the first element of the visualization area;
pick whichever balances better. In **portrait** the slicer area is a shallow
top row, so the hero does *not* belong there — put it in the visualization
area. The logo stays top-left either way.

**Slicer kind by cardinality:**

| Distinct values | `kind` | Height |
|---|---|---|
| under 20 | `buttons` | size to the pills — never let them scroll |
| 20 or more | `dropdown` | **68px** |

Range kinds are sized by their controls, not their cardinality: `daterange`
is **96px**, `range` matches it.

Check cardinality before choosing — `POST /query` with
`EVALUATE SUMMARIZECOLUMNS(Table[Column])` and count the rows. A `buttons`
slicer over a high-cardinality column collapses into a "+N more" pill and is
useless. Note that a slicer's `measure` trim (see
[elements.md](elements.md)) drops options where the metric is null, which can
pull a column back under the threshold — a 141-row date table trimmed to the
three loaded years is a `buttons` slicer, not a dropdown.

**Align the row bands to the rail.** Slicers are sized to their content, so
the rail's edges fall where they fall — and a visualization area split into
equal rows will not line up with any of them, which reads as two unrelated
grids side by side. Choose the row heights so their edges land on rail edges
instead. Equal rows are the starting point, not the goal: unequal bands that
share horizontal lines with the slicers look deliberate, equal ones that
share none do not. You will not hit every line — aim for the row boundaries,
and let the KPI band be the one that floats if something has to.

**Visualization area** — split by element count:

- **1** element: fills the area.
- **2** elements: split in half, `G` between them. Halve along the longer
  axis.
- **3–4** elements: split again into quadrants. With 3, one element spans a
  full half.

A cell can hold a **virtual sub-area** rather than a single visual — a row of
KPI cards, or a chart above a stack of cards. Sub-areas obey the same split
formula and the same `G`, so their outer edges stay flush with their
neighbours.

## Portrait structure

Invert the axes. The slicer area is a **row across the top**, filled left to
right, wrapping to a second row beneath it if needed. The logo takes the
left end of that row; the hero moves into the visualization area, which is
everything below. Every other rule — margin, gap, splitting, rounding,
slicer kinds — is unchanged.

## Checklist

- [ ] Canvas is one of the sizes above.
- [ ] One `G` (default 8), used for the outer margin and every inter-element
      gap.
- [ ] No element touches a canvas edge.
- [ ] Slicers are in their own column (landscape) or row (portrait), stacked
      in order with `G` between them.
- [ ] Logo top-left; hero beside it or leading the visualization area (the
      latter always, in portrait).
- [ ] Dropdown slicers 68px, `daterange` 96px; button slicers only over
      low-cardinality columns, and tall enough that no pill is cut off.
- [ ] No avoidable scrollbar — measure the rendered elements, don't assume,
      and treat a 1px overflow as an overflow.
- [ ] Visualization row bands share horizontal lines with the rail's slicer
      edges.
- [ ] Every shared edge computes to the same pixel — check the arithmetic,
      including the rounding remainder.
