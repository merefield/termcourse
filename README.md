# termcourse

A terminal UI for browsing and posting to Discourse forums. It behaves like a lightweight browser client and supports reading topic lists, viewing full topics, replying, liking, and searching.

## Features

- Browse latest/hot/new/unread/top topic lists.
- Read full topics with scrollable posts and a progress bar.
- Create new topics from the topic list.
- Choose a category when creating a new topic.
- Reply to topics or specific posts (Markdown supported).
- Like/unlike posts.
- Search posts and jump directly to the matching topic context.
- Inline composer with cursor movement, line breaks, and a live character counter.
- Emoji replacements for common `:emoji:` tokens and `:)`-style smiles.
- Username/email + password login (cookie-based session login; supports TOTP/backup codes).
- API key + username login (fallback for SSO-only or locked-down sites).

## Quickstart

```bash
git clone https://github.com/merefield/termcourse
cd termcourse
bundle install

# Option A: username/password login
DISCOURSE_USERNAME="you@example.com" DISCOURSE_PASSWORD="your_password" \
  bundle exec bin/termcourse --login https://your.discourse.host

# Option B: API key fallback
DISCOURSE_API_KEY="your_key" DISCOURSE_API_USERNAME="your_username" \
  bundle exec bin/termcourse https://your.discourse.host
```

## Auth

### Option A: Username + Password (recommended for portability)

This uses a cookie-based browser session and works across most Discourse installs that allow local login.

```bash
DISCOURSE_USERNAME="you@example.com" DISCOURSE_PASSWORD="your_password" \
  bundle exec bin/termcourse --login https://your.discourse.host
```

If MFA (TOTP) is enabled, you’ll be prompted for a 6-digit code. If backup codes are enabled, you can choose that method instead.

### Option B: API Key (fallback)

```bash
DISCOURSE_API_KEY="your_key" DISCOURSE_API_USERNAME="your_username" \
  bundle exec bin/termcourse https://your.discourse.host
```

## ENV

You can set any of these in your shell or `.env` file. `.env` is auto-loaded if present.

- `DISCOURSE_API_KEY`: API key for fallback auth.
- `DISCOURSE_API_USERNAME`: Username tied to the API key.
- `DISCOURSE_USERNAME`: Username or email for password login.
- `DISCOURSE_PASSWORD`: Password for password login.
- `TERMCOURSE_LOGIN_DEBUG`: Set to `1` to log login responses to `/tmp/termcourse_login_debug.txt`.
- `TERMCOURSE_LINKS`: Set to `0` to disable OSC8 clickable links.
- `TERMCOURSE_EMOJI`: Set to `0` to disable emoji substitutions.

Example `.env`:

```bash
DISCOURSE_USERNAME=you@example.com
DISCOURSE_PASSWORD=your_password
```

## How To Use

### Topic List
- Use Up/Down arrows to navigate.
- Press Enter to open a topic.
- Press `n` to create a new topic.
- Press `s` to search.
- Press `f` to cycle the list filter (Latest, Hot, New, Unread, Top).
- Press `p` to change Top period (daily, weekly, monthly, quarterly, yearly).
- Press `g` to refresh.
- Press `q` to quit.

The status bar shows the current list filter and your logged-in username.

### Composer
- Enter inserts a new line.
- Arrow keys move the cursor within the editor.
- Backspace deletes.
- `Ctrl+D` submits.
- `Esc` cancels.

### Topic View
- Up/Down moves between posts.
- Left/Right scrolls the expanded post content.
- `l` like/unlike a post.
- `r` reply to the topic.
- `p` reply to the selected post.
- `s` search from within a topic.
- `esc` goes back to the list.
- `q` quits.

The bottom bar shows your position in the topic (current/total).

### Search
- Press `s` to open search.
- Type your query; Enter runs the search.
- Arrow keys move through results; Enter opens the topic at the matching post.

## Debug & Logging

- Login debug logs are **opt-in**: set `TERMCOURSE_LOGIN_DEBUG=1`.
- Logs are written to `/tmp/termcourse_login_debug.txt`.
- Logs may include usernames and server responses. Disable when not needed and delete after use.

## Security

- **Prompt-based login is supported.** Use `--login` to avoid putting passwords on the command line (which can appear in shell history or process lists).
- **Session cookies are in-memory only.** The app does not write cookies to disk; closing the app ends the session on this client.
- **No password storage.** Credentials are only used for the login request and are not persisted by the app.
- **Some sites disable local login.** If a site requires SSO or blocks scripted login, use an API key or a dedicated test account.
- **MFA support is limited to TOTP/backup codes.** Hardware keys (WebAuthn/passkeys) are not supported in terminal mode.

## Notes

- Replies support Markdown.

## Troubleshooting

- If a site returns login errors with MFA enabled, ensure TOTP is configured and enter a fresh 6-digit code when prompted.
- If you need to force username/password login even when API keys exist, use `--login`.
