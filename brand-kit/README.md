# StreamGremlin Brand Kit

The mascot is a green workshop gremlin with brass goggles and a dark-blue engineer outfit.

## Contents

All files sit directly in this folder.

| File | What it is |
|---|---|
| `StreamGremlin Workshop Banner.png` | Wide banner with the full-body mascot, wordmark and workshop background. 1983×793 PNG with an opaque background. Use it for the repository README, website header or social cards. |
| `streamgremlin-icon-{16,32,48,64,128,256,512,1024}.png` | App icons of the gremlin's head, as square PNGs with transparent backgrounds. Use the 16 and 32 px sizes for favicons and tray icons. |
| `streamgremlin.css` | The UI theme as CSS custom properties, with `dark` (the default), `light` and `midnight` themes. See `web-ui-theming.txt`. |
| `tokens.json` | The same palette and theme values as JSON, for tooling and the desktop app. |
| `ui-theme-preview.html` | A demo page with a theme selector. Open it in a browser from this folder, because it loads `streamgremlin.css` next to it. |
| `contrast-checks.json` | Measured contrast ratios for text and buttons in each theme. |
| `brand-guidelines.txt` | Personality, palette, typography and accessibility rules. |
| `web-ui-theming.txt` | How to apply the themes and the semantic token contract (`--sg-*`). |

## Not in this kit yet

- No separate wordmark file. The lettering appears only inside the banner.
- No standalone transparent full-body mascot. The full-body art appears only in the banner.
- No SVG files. Everything is raster PNG, so don't label any of it as editable vector art.

`brand-guidelines.txt` and `web-ui-theming.txt` still name files and folders from an earlier version of the kit, such as `logos/`, `mascot/`, `themes/` and `*.svg`. Use the table above for what actually exists.

## Colors

- Deep navy: `#0F2A3A`
- Steel blue: `#2F4C5E`
- Muted teal: `#4FA697`
- Gremlin green: `#7FBF6E`
- Antique brass: `#C99A52`
- Warm ivory: `#ECE7DC`
- Ocean blue: `#3E6D8C`

## Recommendations

- Use the banner at the top of the repository README.
- Use the square icons for the app, favicon and tray.
- In the UI, use the semantic `--sg-*` tokens rather than raw palette values. Keep green and brass for the mascot and decoration, and teal for actions.
