This is a terminal app (TUI), just a bit of fun … and still a bit experimental!

| | | |
|---|---|---|
| :information_source: | Summary | A terminal UI for browsing and posting to Discourse forums, now rebuilt in Go on the Charm stack. |
| :hammer_and_wrench: | Repository | [github.com/merefield/termcourse](https://github.com/merefield/termcourse) |
| :open_book: | Install guide | [Install and run](https://github.com/merefield/termcourse#install-and-run) in the README |
| :heart: | Sponsorship | Please consider [sponsoring my open-source work](https://github.com/sponsors/merefield) at a level that suits your or your organisation’s resources and needs, helping this project receive the maintenance it deserves and continue working for your site. |

Enjoying termcourse? Please :star: it [on GitHub](https://github.com/merefield/termcourse).

## Overview

![image|690x345](upload://aFQpYpZ7YkQ9PCTQHO9DGGtsKwa.png)

`termcourse` is a terminal-based Discourse client, rebuilt as a single Go executable. It can use a lightweight browser-style cookie session with username/email and password, including TOTP and backup-code MFA. API-key authentication is available for sites where interactive login is unsuitable.

The interface uses the current Charm stack and works with both keyboard and mouse. Its folder-style navigation, contextual filters, responsive panels, themed controls, Markdown rendering and inline images are designed to make browsing a forum comfortable without leaving the terminal.

## Features

- Browse Latest, Hot, New, Unread, Top and Private Message topic lists, with Top period cycling.
- Navigate persistent Topics, Search, Notifications and Compose folders, with contextual second-level filters.
- Use the keyboard throughout, or click tabs, topic rows, footer controls and hover-highlighted buttons.
- Open visible topics with Enter or number keys `1`–`0`.
- Read complete topics with lazy post loading, compact excerpts, expanded selected posts and responsive scrolling.
- Click a topic’s progress track to jump directly to that point in the post stream.
- Create topics, choose categories, reply to topics or individual posts, and like or unlike posts.
- Search posts and jump directly to the matching post in its topic context.
- Browse and filter notifications, including unread and private-message badges.
- Compose multiline content with cursor movement, insertion, wrapping, paste support and live validation.
- Render GFM Markdown including links, lists, quotes, code, task lists and tables.
- Show high-quality inline and fullscreen images with the Kitty graphics protocol, with colored `chafa` symbols or `viu` as portable fallbacks.
- Receive realtime topic-list, topic, notification and private-message updates when using a cookie session.
- Use per-site credentials from the environment or `credentials.yml`, with prompting for missing login fields.
- Choose from `default`, `slate`, `fairground`, `rust` and `hacker` themes, add YAML themes, and cycle themes while the app is running.
- Use truecolor, 256-color or 16-color output with automatic terminal capability detection.
- Run the interface in English, French, German or Spanish.
- Resize the terminal freely: layouts, colours, topic lists and Kitty images respond to the available space.
- See server-provided retry timing when Discourse rate limits an action, with optional HTTP, UI and image diagnostics.

## Install and run

Go 1.26.6 or newer is required when installing from source. The shortest route is:

```sh
go install github.com/merefield/termcourse/cmd/termcourse@latest
termcourse your.discourse.host
```

Termcourse prompts for a username and password when credentials have not already been configured. Password input is hidden.

To build a local executable from a checkout instead:

```sh
git clone https://github.com/merefield/termcourse.git
cd termcourse
make build
./termcourse your.discourse.host
```

For repeat use, put login details in a local `.env` or use the per-host `credentials.yml` described in the [README](https://github.com/merefield/termcourse#configuration).

### Username/password login (recommended)

Username/password login enables realtime updates:

```sh
DISCOURSE_USERNAME="you@example.com" \
DISCOURSE_PASSWORD="your_password" \
termcourse your.discourse.host
```

### API-key fallback

```sh
DISCOURSE_API_KEY="your_key" \
DISCOURSE_API_USERNAME="your_username" \
termcourse your.discourse.host
```

See the latest [README](https://github.com/merefield/termcourse/blob/master/README.md) for configuration, themes, controls, image backends and troubleshooting.

## Authentication notes

- Username/password login follows Discourse’s CSRF and cookie flow and enables realtime MessageBus updates.
- TOTP and backup-code MFA are supported.
- API-key authentication retains HTTP functionality but does not establish a realtime browser session.
- Some sites disable or restrict scripted username/password login; API credentials are the fallback for those sites.

## Security

- Termcourse does not write prompted credentials or session cookies to disk; session cookies remain in memory.
- Password prompting keeps the password out of shell history.
- Persistent credentials are optional and remain under the user’s control in environment or YAML files.
- Diagnostic logging is opt-in, disabled by default, and does not log credentials or response bodies.

## Limitations

- Sites that forbid remote login flows may require API-key authentication.
- Realtime updates require username/password cookie authentication.
- Native inline image quality depends on terminal support; Kitty is preferred, with symbol rendering available elsewhere.
- It lives in the terminal. :slight_smile:

## Credits

Partly inspired by [Dumbcourse: old browser friendly UI at dumb/d-pad/small screens](https://meta.discourse.org/t/dumbcourse-old-browser-friendly-ui-at-dumb-d-pad-small-screens/395104). :clap:
