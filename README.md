# termcourse

[![Latest release](https://img.shields.io/github/v/release/merefield/termcourse?display_name=tag&sort=semver&label=release)](https://github.com/merefield/termcourse/releases/latest)
[![CI](https://github.com/merefield/termcourse/actions/workflows/ci.yml/badge.svg)](https://github.com/merefield/termcourse/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/merefield/termcourse)](go.mod)
[![License](https://img.shields.io/github/license/merefield/termcourse)](LICENSE)

Termcourse is a Go 1.26.6 terminal UI for browsing and posting to Discourse forums. It replaces the original Ruby implementation while retaining its browsing, posting, realtime, rendering, localization, theming, and image features.

## Features

- Browse Latest, Unread, Private Messages, Hot, New, and Top topic lists.
- Cycle Top periods: daily, weekly, monthly, quarterly, and yearly.
- Rounded, paginated compact/category/stats topic-list layouts, including PM-specific users/replies columns.
- Read complete topics with lazy post-stream loading, scrolling, read-state updates, and a progress footer.
- Create topics, select categories, reply to topics or posts, and like/unlike posts.
- Search posts and open the matching post context.
- Browse/filter notifications and jump to their topic/post.
- Persistent folder tabs above each screen panel: Topics, Search, Notifications, and Compose. Open topics and images remain within the destination that led to them.
- A contextual second rail for topic/notification filters plus search and composition stages, with one spacer row below the masthead.
- Theme-colored folder rails and responsive, clickable footer controls for screen-specific hotkeys, with pointer-hover highlighting and the active theme anchored at bottom right.
- Mouse-clickable tabs, footer controls, and rows plus wheel scrolling, with keyboard navigation retained throughout.
- Cookie login with username/password, TOTP, or backup codes.
- API-key fallback for sites where browser login is unsuitable.
- MessageBus list/topic updates, notification and PM badges, resume positions, and watchdog recovery for cookie sessions.
- Inline multiline composer with cursor movement, line breaks, character validation, submit/cancel controls.
- Built-in English, French, German, and Spanish UI translations.
- Built-in `default`, `slate`, `fairground`, `rust`, and `hacker` themes plus YAML overrides.
- Truecolor, 256-color, and 16-color output.
- GFM Markdown rendering (including lists, quotes, code, tasks, and tables), OSC8 links, emoji substitution, and ANSI/grapheme-aware sizing.
- High-quality inline/fullscreen images through Kitty Unicode placements, with colored `chafa` symbols (or `viu`) as the portable fallback and explicit size/quality limits.
- Incremental screen repainting and resize-responsive layouts, with a versioned branded masthead, wide-terminal logo, rounded titled panels, and themed block gauges.
- Rate-limit errors show the server-provided retry duration and local deadline when available, and explicitly identify untimed responses.

## Install and run

### Prebuilt release (recommended)

On Linux or macOS, the release installer selects the archive for the current operating system and architecture, verifies its SHA-256 checksum, checks the binary's reported version, and installs it as `/usr/local/bin/termcourse`:

```sh
curl -fsSL https://raw.githubusercontent.com/merefield/termcourse/master/install-release.sh | sh
```

The installer requires `curl` or `wget`, `tar`, and either `sha256sum` (Linux) or `shasum` (macOS). It uses `sudo` only when the destination is not writable. For a user-local installation that does not require `sudo`:

```sh
curl -fsSL https://raw.githubusercontent.com/merefield/termcourse/master/install-release.sh |
  TERMCOURSE_BIN_DIR="$HOME/.local/bin" sh
```

Ensure `$HOME/.local/bin` is on `PATH` when using that location. To install a particular release reproducibly:

```sh
curl -fsSL https://raw.githubusercontent.com/merefield/termcourse/master/install-release.sh |
  sh -s -- --version v0.2.1
```

You can download and inspect [install-release.sh](install-release.sh) before running it. The installer supports `--help`, `--version TAG`, and `--bin-dir DIR`; the equivalent environment variables are `TERMCOURSE_VERSION` and `TERMCOURSE_BIN_DIR`.

Prebuilt releases do not require Go. Each [GitHub Release](https://github.com/merefield/termcourse/releases) contains these assets:

| Operating system | Architectures | Archive | Installation |
| --- | --- | --- | --- |
| Linux | AMD64, ARM64 | `.tar.gz` | Installer or manual |
| macOS | Intel (AMD64), Apple Silicon (ARM64) | `.tar.gz` | Installer or manual |
| Windows | AMD64, ARM64 | `.zip` | Manual |

For a manual installation, verify the selected archive against the release's `checksums.txt`, extract `termcourse` (or `termcourse.exe` on Windows), and place it on `PATH`.

Run Termcourse with the hostname or URL of any Discourse site:

```sh
termcourse meta.discourse.org
```

If no credentials are configured, Termcourse prompts for the missing username and password. Password input is hidden. Confirm the installed release at any time with `termcourse --version`.

### Install with Go

Go 1.26.6 or newer is required when installing from source. `go install` compiles Termcourse locally:

```sh
go install github.com/merefield/termcourse/cmd/termcourse@latest
termcourse meta.discourse.org
```

If the shell cannot find `termcourse`, add the Go binary directory to `PATH`. `go install` uses `GOBIN` when configured and otherwise uses `$(go env GOPATH)/bin`:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

### Build from a checkout

```sh
git clone https://github.com/merefield/termcourse.git
cd termcourse
make build
./termcourse meta.discourse.org
```

`make build` creates `./termcourse` in the repository root. Without `make`, use `go build -o ./termcourse ./cmd/termcourse`. You can also run directly from a checkout without keeping a binary:

```sh
go run ./cmd/termcourse meta.discourse.org
```

`make install` is also available and honours `DESTDIR` and `PREFIX`:

```sh
make install PREFIX="$HOME/.local"
```

For repeat use, credentials can be supplied in `.env` or the host-mapped credentials file described under [Configuration](#configuration). The examples below use an installed `termcourse`; replace it with `./termcourse` when running a binary built in the repository. Username/password login enables realtime MessageBus updates:

```sh
DISCOURSE_USERNAME="you@example.com" \
DISCOURSE_PASSWORD="your_password" \
termcourse --theme slate --lang fr https://your.discourse.host
```

API-key login (HTTP features only):

```sh
DISCOURSE_API_KEY="your_key" \
DISCOURSE_API_USERNAME="your_username" \
termcourse --theme fairground https://your.discourse.host
```

List the built-in and configured themes, or preview one theme:

```sh
termcourse themes
termcourse themes hacker
```

A local `.env` is loaded automatically. CLI credentials override host credentials from YAML, which override generic environment variables. If both login and API pairs exist, login is tried first unless the host entry selects `auth: api`.

For contributors, `make check` runs formatting validation, vet, race-enabled Go tests, installer integration tests, and a local build.

## Releases and versioning

Termcourse uses semantic Git tags such as `v0.2.1` as the release-version source of truth. Go embeds that module version in binaries installed with `go install`; GoReleaser injects it into release binaries; and `make build` injects the current `git describe` value. `termcourse --version` and the wide masthead subtitle use the same resolved build version. Untagged direct development builds append their embedded commit and dirty state to the development version declared in [termcourse.go](termcourse.go).

[GoReleaser](.goreleaser.yaml) builds static Linux, macOS, and Windows archives for AMD64 and ARM64, plus `checksums.txt`. Test the configuration locally without publishing:

```sh
goreleaser release --snapshot --clean --skip=publish
```

Pushing a semantic-version tag runs [the release workflow](.github/workflows/release.yml). It validates the tag syntax and confirms the tagged commit is reachable from `master`, runs the complete check suite, verifies that the tag did not move between validation and publication, and then creates the GitHub Release. No package manager, container registry, or announcement publisher is configured.

After this release workflow reaches `master`, create `v0.2.1` from the intended release commit. The existing `v0.2.0` tag remains immutable and has no generated binary release:

```sh
git tag -a v0.2.1 -m "termcourse v0.2.1"
git push origin v0.2.1
```

An existing unpublished tag containing the release configuration can also be published explicitly with `gh workflow run release.yml --ref master -f tag=TAG`.

## Migrating from the Ruby version

The Go version is a replacement rather than a separately configured application. Existing `.env` files, generic Discourse credential variables, and host entries in `credentials.yml` remain compatible. Authentication precedence is unchanged, so most users can replace `bundle exec bin/termcourse HOST` with `termcourse HOST` and keep their credentials as they are.

Be mindful of these differences:

- Ruby, Bundler, and the gem bundle are no longer required. The Go build produces one `termcourse` executable; use `termcourse` for an installed binary or `./termcourse` for one built in the repository.
- `./theme.yml` is no longer discovered automatically. Move it to the platform user configuration directory (`~/.config/termcourse/theme.yml` on Linux), set `TERMCOURSE_THEME_FILE`, or pass `--theme-file PATH`. This prevents the active theme changing with the launch directory.
- Existing theme names, all 12 color fields, partial overrides, and the legacy top-level theme-map YAML format remain supported. The newer format can also contain `theme: NAME` and a `themes:` map. Remove or leave `TERMCOURSE_THEME` blank if the file's `theme:` selection should take effect.
- Theme files are now checked strictly. Unknown fields, invalid colors, unreadable explicitly selected files, and unknown themes stop startup with an explanatory error instead of being silently ignored or replaced with the default theme.
- If an old `.env` contains `TERMCOURSE_IMAGE_MODE=stable`, replace it with `balanced`. The supported values are `compat`, `balanced`, and `high`.
- The default symbol-thumbnail size changed from 14 lines to 48 columns by 6 lines. Existing explicit `TERMCOURSE_IMAGE_LINES` values still work; use `TERMCOURSE_IMAGE_COLUMNS` to set the width.
- Automatic color handling now detects terminal capabilities instead of using the Ruby version's platform heuristic. `TERMCOURSE_COLOR_MODE=truecolor`, `256`, or `16` still forces a specific mode.
- New optional controls include `TERMCOURSE_MOUSE`, `TERMCOURSE_IMAGE_PROTOCOL`, and `TERMCOURSE_IMAGE_COLUMNS`. They require no migration because their defaults preserve automatic behaviour.

There is no new monolithic Go configuration file. Configuration remains split between environment variables, credentials YAML, and theme YAML, and no effective Ruby theme color or authentication option has been removed.

## Configuration

The program looks for host credentials in:

1. `TERMCOURSE_CREDENTIALS_FILE`
2. `./credentials.yml`
3. `~/.config/termcourse/credentials.yml`

See [credentials.example.yml](credentials.example.yml) and [.env.example](.env.example).

Theme selection uses the first available value:

1. `--theme NAME`
2. `TERMCOURSE_THEME`
3. The `theme:` value in the theme file
4. `default`

The five built-in themes are `default`, `slate`, `fairground`, `rust`, and `hacker`; they work without any files. Theme files are loaded from `--theme-file PATH`, then `TERMCOURSE_THEME_FILE`, then the platform user configuration directory (`~/.config/termcourse/theme.yml` on Linux). The launch directory is deliberately not consulted.

See [theme.example.yml](theme.example.yml) for partial built-in overrides and custom themes. Supported keys are `primary`, `background`, `highlighted`, `highlighted_text`, `borders`, `bar_backgrounds`, `separators`, `list_numbers`, `list_text`, `post_username`, `list_meta`, and `accent`. Colors accept `#rrggbb`, indexes `0`–`255`, `black`, `white`, `red`, `green`, `blue`, `yellow`, `cyan`, `magenta`, `gray`/`grey`, or `none`. Invalid fields, colors, files, and theme names produce actionable errors.

### Image rendering

With `TERMCOURSE_IMAGE_PROTOCOL=auto`, Termcourse probes for Kitty graphics support and uses Unicode virtual placements when available in truecolor mode. This gives inline thumbnails that remain part of the terminal cell layout, plus resize-responsive fullscreen images. Kitty commands are passed through tmux and GNU Screen automatically.

When Kitty is unavailable, Termcourse uses `chafa` for colored symbol rendering and can use Sixel for fullscreen output on compatible terminals. If `chafa` is not installed, `viu` is the secondary fallback. These programs are optional external tools and must be available on `PATH`; image-free operation needs no external image-rendering tool.

Use `TERMCOURSE_IMAGE_PROTOCOL=kitty` to force Kitty or `TERMCOURSE_IMAGE_PROTOCOL=symbols` to disable it. `TERMCOURSE_IMAGE_BACKEND` selects the fallback tool. Image downloads retain the active Discourse authentication and are constrained by the configured byte, pixel, and terminal-cell limits.

### Environment

| Variable | Purpose |
| --- | --- |
| `DISCOURSE_USERNAME`, `DISCOURSE_PASSWORD` | Cookie login credentials. |
| `DISCOURSE_API_KEY`, `DISCOURSE_API_USERNAME` | API authentication fallback. |
| `TERMCOURSE_CREDENTIALS_FILE` | Credentials YAML override. |
| `TERMCOURSE_THEME`, `TERMCOURSE_THEME_FILE` | Theme name/file; overridden by `--theme` and `--theme-file`. |
| `TERMCOURSE_LANG` | `en`, `fr`, `de`, or `es`; then `LC_ALL`, `LC_MESSAGES`, `LANG`. |
| `TERMCOURSE_COLOR_MODE` | `auto`, `truecolor`, `256`, or `16`; auto detects output capabilities. |
| `TERMCOURSE_LINKS`, `TERMCOURSE_EMOJI` | Set to `0` to disable. |
| `TERMCOURSE_MOUSE` | Set to `0` to disable click/wheel capture and retain ordinary terminal text selection. |
| `TERMCOURSE_IMAGES` | Set to `0` to disable previews. |
| `TERMCOURSE_IMAGE_PROTOCOL` | `auto`, `kitty`, or `symbols`; auto probes Kitty and falls back safely. |
| `TERMCOURSE_IMAGE_BACKEND` | `auto`, `chafa`, `viu`, or `off`. |
| `TERMCOURSE_IMAGE_MODE` | `compat`, `balanced` (default), or `high` for symbol fallback. |
| `TERMCOURSE_IMAGE_COLORS` | `auto`, `none`, `16`, `240`, `256`, or `full`. |
| `TERMCOURSE_IMAGE_COLUMNS`, `TERMCOURSE_IMAGE_LINES` | Maximum thumbnail size, defaults 48×6 cells. |
| `TERMCOURSE_IMAGE_MAX_BYTES` | Per-image limit, default 5,242,880. |
| `TERMCOURSE_IMAGE_QUALITY_FILTER` | Set to `0` to allow noisy previews. |
| `TERMCOURSE_TICK_MS` | Input/resize poll interval, default 100ms. |
| `TERMCOURSE_HTTP_DEBUG` | Set to `1` for request status, timing, retry, and rate-limit diagnostics. |
| `TERMCOURSE_DEBUG`, `TERMCOURSE_IMAGE_DEBUG` | Set to `1` for UI/MessageBus or image diagnostics. |

## Controls

Global navigation:

- `t` cycles through the built-in and configured themes; the footer button is also available while typing.
- `Tab` and `Shift+Tab` move between the four first-row destinations.
- Click a primary or contextual folder tab to select it.
- Click a responsive footer button or use its displayed keyboard shortcut.
- Click a post or row to select it; click a selected list row again to open it.
- The mouse wheel moves through lists and scrolls the expanded post body.
- Hold the terminal's mouse-bypass modifier (commonly Shift) for text selection, or set `TERMCOURSE_MOUSE=0`.
- Navigation is visibly locked while editing topic titles and post bodies so an accidental click cannot discard a draft; the transient search query remains navigable.

Topic list:

- Arrows move; Enter or `1`–`0` opens.
- `c` creates, `n` opens notifications, `s` searches.
- `f` cycles filters; `p` cycles Top periods.
- `g` refreshes; `q` or Escape quits.

Topic view:

- Up/Down selects posts; Left/Right scrolls the expanded post.
- Click the read-progress track to jump to the corresponding position in the complete topic.
- `l` toggles like; `r` replies to the topic; `p` replies to the post.
- `s` searches; `n` opens notifications; `x` opens an image.
- Escape/Backspace goes back; `q` quits.

Fullscreen image:

- `x` or Escape closes the image and restores the topic view.
- Kitty fullscreen images redraw at the new terminal dimensions when the terminal is resized.

Composer:

- Enter adds a line; arrows move; Backspace deletes.
- Ctrl+D submits; Escape cancels.

Notifications and search use arrows, Enter to open, Escape to return, and `q` to quit. Notifications use `f` to cycle filters; search results use `n` to open notifications.

## Authentication notes

Realtime updates require a browser-style cookie session, so they are enabled after username/password login. API-key mode retains all HTTP operations but intentionally does not create a realtime session. Login auth follows Discourse's CSRF/cookie flow and prompts for TOTP or backup codes when the server requests a second factor.

## Rate limits and diagnostics

For Discourse rate-limit responses, Termcourse prefers the HTTP `Retry-After` value and falls back to JSON `extras.wait_seconds` or `extras.time_left`. The error panel shows a countdown and local retry time when possible. It says explicitly when the server reports that retry is already available or when the server provides no timing information; Termcourse does not invent an unreliable delay.

Set `TERMCOURSE_DEBUG=1` to include the server's `Discourse-Rate-Limit-Error-Code` in the error panel. More detail is available through these opt-in logs in the system temporary directory:

| Variable | Log file |
| --- | --- |
| `TERMCOURSE_HTTP_DEBUG=1` | `termcourse_http_debug.txt` |
| `TERMCOURSE_DEBUG=1` | `termcourse_debug.txt` |
| `TERMCOURSE_IMAGE_DEBUG=1` | `termcourse_image_debug.txt` |

On Linux the system temporary directory is normally `/tmp`, unless `TMPDIR` selects another location. HTTP diagnostics include response status, request duration, `Retry-After`, and the Discourse limiter code; credentials and response bodies are not logged.

## Implementation

The interface runs on the current Charm v2 stack. Bubble Tea owns raw mode, the alternate screen, synchronized incremental rendering, resize events, cursor state, terminal queries, color downsampling, window metadata, and supported native progress metadata. Bubbles supplies the themed single-line editor and multiline composer, including bracketed paste, word navigation, soft wrapping, cursor behavior, and viewport scrolling. Lip Gloss v2 provides pure theme/layout styles, while Glamour v2/Goldmark renders GFM and `x/ansi` provides ANSI-safe grapheme measurement, truncation, Kitty graphics encoding, capability responses, and virtual image placement. `x/term` remains limited to pre-TUI password input, terminal detection, and sizing fallback.

The Discourse MessageBus client remains protocol-specific because MessageBus uses chunk-framed HTTP long polling rather than WebSockets. Its lifecycle and resume semantics remain domain code, while terminal I/O is delegated to Bubble Tea.

## License

Termcourse is available under the [MIT License](LICENSE). See [COPYRIGHT](COPYRIGHT)
for the copyright notice.
