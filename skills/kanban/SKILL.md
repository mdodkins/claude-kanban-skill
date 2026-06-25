---
name: kanban
description: >
  A self-hosted kanban board with five columns (To Do, Blocked, In Progress, In
  Review, Done), persisted to a JSON file. TRIGGER when the user asks to add,
  move, edit, or list cards on the kanban board, to start the board server, or
  to break a piece of work down into cards. The board ships with a Go server in
  this plugin's `server/` directory.
---

# Kanban

A tiny self-hosted kanban board. The user moves cards by drag-and-drop in their
browser. You (Claude) seed and query cards via a small HTTP API.

## Starting the server

If the user asks to start the board, run the Go server from the plugin's
`server/` directory:

```bash
cd <plugin-path>/server
go run . --listen 127.0.0.1:8765 --state ~/.kanban/state.json
```

Tell the user to open http://127.0.0.1:8765/ in their browser. The state file
(default `~/.kanban/state.json`) is append-friendly and survives restarts.

Flags:

- `--listen` (default `127.0.0.1:8765`): host:port to bind.
- `--state`  (default `~/.kanban/state.json`): JSON state file. Will be created
  on first write.

If the server is already running and the user wants to add or edit cards, you
don't need to restart it; just talk to the API.

## Five columns (fixed)

The frontend renders five columns in this order, identified by these literal
strings:

| ID            | Label        |
|---------------|--------------|
| `to-do`       | To Do        |
| `blocked`     | Blocked      |
| `in-progress` | In Progress  |
| `in-review`   | In Review    |
| `done`        | Done         |

Use the IDs exactly when setting the `column` field over the API.

## HTTP API

Base URL: whatever the user has the server listening on, e.g.
`http://127.0.0.1:8765`. All bodies are JSON.

### `GET /api/cards`

Returns `[]Card` (every card on the board, in arbitrary order). The frontend
sorts by column then position.

### `POST /api/cards`

Create one card. Body:

```json
{ "title": "Required, max 200 chars",
  "description": "Optional, max 4000 chars",
  "column": "to-do | blocked | in-progress | in-review | done" }
```

Returns `201` and the created card. `400` if `title` is empty.

### `PATCH /api/cards/{id}`

Sparse update. Send only the fields you want to change:

```json
{ "title": "...", "description": "...", "column": "in-progress", "position": 0 }
```

Returns `200` and the updated card. `404` if the ID is unknown.

### `DELETE /api/cards/{id}`

Returns `204`. `404` if the ID is unknown.

### `POST /api/cards/{id}/attachments`

Upload a file attached to a card. Send as `multipart/form-data` with a `file`
field. Max 10 MB per file. Returns `201` and the attachment metadata:

```json
{ "id": "abc123", "filename": "diagram.png", "mime_type": "image/png", "size": 42000 }
```

### `GET /api/cards/{id}/attachments/{aid}`

Download an attachment. Returns the file with `Content-Disposition: attachment`.

### `DELETE /api/cards/{id}/attachments/{aid}`

Returns `204`. Removes the metadata and the file from disk.

## Seeding many cards

When the user asks you to break a piece of work into cards and put them on the
board, write the breakdown first (numbered so the order is obvious), then POST
each one. Use the title for the short name and the description for the
detail. Prefix card titles with the order number if order matters.

Example, batched with a small shell loop:

```bash
for entry in \
  "1. Set up DNS|Add kanban.pitchforks.net A record" \
  "2. Open ports|Cloud firewall TCP 80+443"; do
  IFS='|' read -r title desc <<< "$entry"
  curl -sS -X POST -H 'Content-Type: application/json' \
    -d "$(jq -n --arg t "$title" --arg d "$desc" '{title:$t,description:$d,column:"to-do"}')" \
    http://127.0.0.1:8765/api/cards >/dev/null
done
```

Confirm to the user how many you added and which column they went into. Don't
ask for per-card confirmation; the user can edit or delete any card in the
browser.

## When NOT to use this skill

- Generic to-do tracking that doesn't need a board view: a list in
  conversation is fine.
- The user just wants you to remember a single task: use task tools, not this.
- The user is debugging: this is for organising forward work, not log
  analysis.

## State file

The JSON state file is the source of truth. It is safe to:

- Copy or back up between machines.
- Edit by hand while the server is stopped.
- Delete to start fresh.

Do NOT edit it while the server is running; you'll race with the server's
atomic writes.
