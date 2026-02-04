# termcourse

Terminal UI for browsing and posting to Discourse forums.

## Setup

```bash
bundle install
```

## Auth

Use a Discourse API key and username. You can use a `.env` file during development.

```bash
cp .env.example .env
```

```bash
export DISCOURSE_API_KEY="your_key"
export DISCOURSE_API_USERNAME="your_username"
```

## Run

```bash
bin/termcourse https://meta.discourse.org
```

## Keybindings

Topic list:
- Up/Down: move
- Enter: open topic
- g: refresh
- q: quit

Topic view:
- Up/Down: move between posts
- l: like/unlike post
- r: reply to topic
- p: reply to selected post
- q: back

## Notes

- Replies support Markdown.
- Likes require an API key with write access.
