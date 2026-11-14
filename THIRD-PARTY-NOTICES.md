# Third-party notices

StreamGremlin's desktop app, command line and Stream Deck plugin include the following. Each keeps its own licence. The full text of every Go module's licence is in [THIRD-PARTY-LICENSES.txt](THIRD-PARTY-LICENSES.txt), to ship beside the app and command line in every release; `make verify` fails when a Go module built into StreamGremlin is missing from either file.

## Button icons

Two icon sets are bundled for the Live screen's buttons (D-076, [ADR-0040](docs/adr/0040-buttons.md)). Nothing is fetched from the internet.

### Tabler Icons

<https://tabler.io/icons>, version 3.50.0, MIT licence. Copyright (c) 2020-2026 Paweł Kuna.

### Nerd Fonts symbols

<https://www.nerdfonts.com>, version 3.5.1, the Symbols Only font, cut down to the sets below by `cmd/desktop/frontend/tools/nerd-symbols.py`. Nerd Fonts is MIT licensed, copyright (c) 2014 Ryan L McIntyre. The icons inside it come from these projects, under their own licences:

| Set (name prefix) | From | Licence |
|---|---|---|
| Material Design (`md-`) | <https://github.com/Templarian/MaterialDesign-Font> | Apache 2.0 |
| Font Awesome (`fa-`) | <https://github.com/FortAwesome/Font-Awesome>, 6.5.1 | CC BY 4.0 (icons), SIL OFL 1.1 (font) |
| Font Awesome Extension (`fae-`) | <https://github.com/AndreLZGava/font-awesome-extension> | MIT |
| Devicons (`dev-`) | <https://github.com/devicons/devicon> | MIT |
| Codicons (`cod-`) | <https://github.com/microsoft/vscode-codicons> | CC BY 4.0 |
| Octicons (`oct-`) | <https://github.com/primer/octicons> | MIT |
| Weather Icons (`weather-`) | <https://github.com/erikflowers/weather-icons> | SIL OFL 1.1 |
| Font Logos (`linux-`) | <https://github.com/lukas-w/font-logos> | Unlicense |

Brand logos (Twitch, Discord, YouTube and others) are trademarks of their owners. They're offered for pointing at those services, as the icon sets intend.

## The Stream Deck plugin

The plugin (`streamdeck/`) bundles:
- Elgato's Stream Deck SDK, `@elgato/streamdeck` 3.0.4, and its dependencies (`@elgato/utils`, `@elgato/schemas`, `ws`), MIT licence, copyright (c) Corsair Memory Inc. and their authors;
- zod 4.6.7 (<https://zod.dev>), MIT licence, copyright (c) 2025 Colin McDonnell. Elgato's SDK asks for zod 3; the plugin uses 4.6.7 instead, for a security fix (ADR-0042);
- Tabler Icons 3.50.0, and the Nerd Fonts icons as outlines (both above, with their licences), to draw button icons on keys.

Elgato's CLI (`@elgato/cli`, MIT) packs it and isn't shipped.

## The app's screens

The desktop app's screens are built with Svelte (<https://svelte.dev>, MIT licence, copyright (c) 2016-2026 Svelte contributors).

## Go modules

The app and the command line are written in Go (BSD 3-clause licence, copyright (c) 2009 The Go Authors) and built with these modules. Versions are those in `go.mod` and `cmd/desktop/go.mod`.

| Module | Licence | Copyright | Used for | In |
|---|---|---|---|---|
| `github.com/wailsapp/wails/v3` | MIT | (c) 2018-present Lea Anthony | The desktop app's window | App |
| `github.com/adrg/xdg` | MIT | (c) 2014 Adrian-George Bostan | Wails: standard folders on Linux | App |
| `github.com/go-ole/go-ole` | MIT | (c) 2013-2017 Yasuhiro Matsumoto | Wails: Windows' WebView2 | App on Windows |
| `github.com/mattn/go-colorable` | MIT | (c) 2016 Yasuhiro Matsumoto | Wails: logs on Windows | App on Windows |
| `modernc.org/sqlite` | BSD 3-clause | (c) 2017 The Sqlite Authors | The database | Both |
| SQLite, inside `modernc.org/sqlite` | Public domain | | The database | Both |
| `modernc.org/libc` | BSD 3-clause | (c) 2017 The Libc Authors | SQLite in Go. It carries code from musl libc, go-netdb and NixOS/nixpkgs (MIT) and from Go (BSD), credited in its notices | Both |
| `modernc.org/mathutil` | BSD 3-clause | (c) 2014 The mathutil Authors | SQLite in Go | Both |
| `modernc.org/memory` | BSD 3-clause | (c) 2017 The Memory Authors; mmap-go (c) 2011 Evan Shaw | SQLite in Go | Both |
| `github.com/remyoudompheng/bigfft` | BSD 3-clause | (c) 2012 The Go Authors | SQLite in Go | Both |
| `github.com/ncruces/go-strftime` | MIT | (c) 2022 Nuno Cruces | SQLite in Go | Both |
| `github.com/dustin/go-humanize` | MIT | (c) 2005-2008 Dustin Sallings | SQLite in Go | Both |
| `github.com/google/uuid` | BSD 3-clause | (c) 2009, 2014 Google Inc. | SQLite in Go | Both |
| `github.com/mattn/go-isatty` | MIT | (c) Yasuhiro Matsumoto | SQLite in Go | Both |
| `cel.dev/cel-go` | Apache 2.0 | (c) Google LLC | Workflow conditions | Both |
| `cel.dev/expr` | Apache 2.0 | (c) Google LLC | Workflow conditions | Both |
| `github.com/antlr4-go/antlr/v4` | BSD 3-clause | (c) 2012-2023 The ANTLR Project | Workflow conditions (CEL's parser) | Both |
| `google.golang.org/protobuf` | BSD 3-clause | (c) 2018 The Go Authors | Workflow conditions (CEL); YouTube chat | Both |
| `google.golang.org/grpc` | Apache 2.0 | (c) 2014 gRPC authors | YouTube chat | Both |
| `google.golang.org/genproto/googleapis/api` | Apache 2.0 | Google LLC | Workflow conditions (CEL) | Both |
| `google.golang.org/genproto/googleapis/rpc` | Apache 2.0 | Google LLC | Workflow conditions (CEL); YouTube chat (gRPC) | Both |
| `golang.org/x/exp` | BSD 3-clause | (c) 2009 The Go Authors | Workflow conditions (CEL) | Both |
| `golang.org/x/text` | BSD 3-clause | (c) 2009 The Go Authors | Workflow conditions (CEL); Wails | Both |
| `golang.org/x/net` | BSD 3-clause | (c) 2009 The Go Authors | YouTube chat (gRPC's HTTP/2) | Both |
| `github.com/yuin/gopher-lua` | MIT | (c) 2015 Yusuke Inuzuka | Lua scripts | Both |
| `github.com/coder/websocket` | ISC | (c) 2025 Coder | Twitch EventSub and OBS | Both |
| `github.com/zalando/go-keyring` | MIT | (c) 2016 Zalando SE | Passwords and sign-ins in the system keyring | Both |
| `github.com/danieljoos/wincred` | MIT | (c) 2014 Daniel Joos | The keyring on Windows | Both, on Windows |
| `github.com/godbus/dbus/v5` | BSD 2-clause | (c) 2013 Georg Reinke, Google | The keyring on Linux | Both, on Linux |
| `go.yaml.in/yaml/v3` | MIT and Apache 2.0 | (c) 2006-2011 Kirill Simonov; (c) 2011-2019 Canonical Ltd | Workflow conditions (CEL) | Both |
| `golang.org/x/sys` | BSD 3-clause | (c) 2009 The Go Authors | Operating system calls | Both |
