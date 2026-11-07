
# StreamGremlin Brand Kit v2

## What's new

Replaces the previous low-fidelity detached-head mascot with the higher-quality concept artwork. The gremlin is a coherent full-body character with brass goggles, green skin and a dark-blue engineer outfit.

The wordmark `logos/streamgremlin-wordmark-contrast-outline.png` adds a warm-ivory outline to the original transparent lettering, making dark text legible on navy or black. For white backgrounds the original PNG is also supplied.

## Contents

- `source/`: unmodified high-detail generated artwork (raster masters).
- `mascot/`: transparent PNG versions at 256, 512, 1024, 2048 pixels.
- `icons/`: app/icon PNG exports (16–1024 px).
- `logos/`: contrast-outlined wordmark, original wordmark and navy badge.
- `themes/`: CSS and JSON design tokens.
- `guides/`: brand/UI usage notes and contrast checks.

## Important quality notes

- The 2048-pixel images are high-quality Lanczos resamples of smaller original generated images, not native 2048-pixel illustrations. Their fine detail is limited by the originals.
- The refined artwork is PNG raster art. It is not honest to label those illustrations as truly editable SVG; the previous simplified SVG assets are deliberately not included here.
- The source head illustration has an opaque black background, so prefer the transparent icon/app mark and the mascot for flexible placements.

## Colors

- Navy: `#0F2A3A`
- Steel blue: `#2F4C5E`
- Teal: `#4FA697`
- Gremlin green: `#7FBF6E`
- Antique brass: `#C99A52`
- Warm ivory: `#ECE7DC`

## Recommendations

For GitHub README on white or black use transparent full mascot or contrast-outlined wordmark. App icons use supplied square icons, and UI themes should use semantic tokens rather than raw palette values.

