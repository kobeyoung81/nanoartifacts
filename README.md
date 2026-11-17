# NanoArtifacts

The platform's work library: agents publish self-contained HTML documents over
MCP, users browse and share them in the portal.

## What it owns

- **Its own collection prefix** (`artifacts_`) in the platform database
  `nanosec`, reached through `NANOARTIFACTS_MONGO_URI`. Nothing outside this
  service writes that prefix, and this service reads nothing outside it —
  identity and agent ownership are asked from the bus over HTTP.
- **Metadata**: `artifacts`, `artifacts_versions`, `artifacts_grants`,
  `artifacts_spaces`, `artifacts_nodes`, `artifacts_uploads`.
- **Bodies**: a GridFS-compatible pair (`artifacts_fs.files` /
  `artifacts_fs.chunks`, 255 KiB chunks) written directly, with the content's
  sha256 as the file id. Publishing identical bytes twice therefore stores and
  charges them once.

## Why the ids look the way they do

`artifacts_fs.files._id` is the sha256 of the body. That is what makes dedupe free and a
publish idempotent. Chunk documents use `<hash>:<n>` as `_id` so no BSON
ObjectID has to be generated for content that is already immutable.

## Layout and invariants

- Bodies live in `artifacts_fs.chunks`, keyed by `files_id = sha256`; there is no separate
  sharded directory on disk.

- Blob first, version row second: a crash in between leaves an unreferenced
  body, which `gc` removes after an hour. The reverse order would leave a
  version whose content cannot be read.
- `artifacts_fs.files` is written last during an upload, so a reader never sees half a
  document.
- Deleting is a soft delete (`deleted_at`). The UI offers no recycle bin and
  promises no recovery; an operator can restore by clearing the field.
- `gc` also removes chunks whose file document never appeared (an interrupted
  upload).

## Where it lives

| Path | Owner | Notes |
|---|---|---|
| `/artifacts`, `/artifacts?work=<id>` | the portal | shell + frame; this is the URL people share |
| `/nanoartifacts/` | this service | the framed app, its assets and its API |
| `/nanoartifacts/api/v1/…` | this service | proxied by the portal, prefix stripped |

The two prefixes are deliberately different. When they were both `/artifacts`,
any trailing-slash normalisation in a proxy sent a portal URL to the service —
a new tab then opened without the portal's chrome.

## As a portal plugin

The library also ships as a Module Federation remote, so the portal's workbench
hosts it as two panels — 作品库 (the space switcher and the tree, with the node's
details as a second full view of the same panel) and 文档 (the body) — instead of
a page.

| Path | What |
|---|---|
| `/nanoartifacts/plugin.json` | the plugin manifest: contract version, remote entry, panel list |
| `/nanoartifacts/plugins/nanoartifacts/remoteEntry.js` | the federation remote (`./panels` → `LibraryPanel`, `DocumentPanel`) |
| `/nanoartifacts/plugins/nanoartifacts/assets/*` | the panel bundle (hashed, immutable) |

Two build passes share one source: `vite build` produces the standalone app,
`vite build --config vite.config.remote.ts` produces the remote. The remote build

- scopes every stylesheet rule to `.plugin-nanoartifacts` and drops this app's
  page-level resets (`:root`, `.dark`, `body`) — the palette is the shell's;
- takes React from the host (`shared: { import: false }`), so a panel and the
  shell run one copy; two would break hooks and context. The check is
  `grep -rl "Minified React error" dist/plugins/nanoartifacts/assets` returning
  nothing — with a plain `{ singleton: true }` the build embeds a fallback copy
  and the marker appears, which is what makes the check falsifiable;
- keeps the standalone app out of the bundle (the HTML entry is an empty
  document), so `remoteEntry.js` only carries the panels and the federation
  runtime.

Identity, language and route arrive as props from the shell. Inside the remote,
both panels share one module-level route store, so selecting a node in the tree
opens its document in the other panel. The full page (`/artifacts`) keeps the
hash router and is unaffected.

The panel and the full page are two surfaces of one app, laid out differently:

- **page**: three columns (tree / document / details), resizable, details open by
  default; the ⋯ menu toggles the detail column.
- **panel**: one view at a time. The tree fills the panel; ⋯ → details switches
  the whole panel to the versions/sharing view, and “← 返回作品库” comes back. No
  column, no resize grip, no width animation — the panel is narrow and one screen
  at a time is the honest layout for it.

The UI is split by view (`web/src/library/`): `SidebarView`, `DetailView`,
`DocumentView`, `dialogs`, `tree` and the shared `model` helpers. `app.tsx` is the
page shell plus the state/composition both surfaces use; a panel entry names the
view it wants (`panelView`), and the document panel therefore renders the body
and nothing else. The panel's height chain is host div → `.plugin-nanoartifacts`
→ `.panel-root` → `.panel-view`; every link holds a height because a missing one
makes `height: 100%` resolve against `auto` and collapses an iframe to its 150px
default. Panels carry the page's own 13px type baseline on
`.panel-view`, because the `body`/`.nz` rules that used to supply it are not part
of a panel.

## HTTP

| Method | Path | Notes |
|---|---|---|
| GET | `/healthz` | liveness |
| GET | `/api/v1/artifacts?scope=owned\|shared&q=&kind=` | the caller's JWT decides visibility |
| POST | `/api/v1/artifacts` | publish (`html`, `title`, `kind`, optional `artifact_id`) |
| GET | `/api/v1/artifacts/{id}` | metadata + versions + grants |
| POST | `/api/v1/session` | exchanges the JWT for the preview cookie (`HttpOnly`, `SameSite=Lax`) |
| GET | `/api/v1/artifacts/{id}/versions/{seq}/raw?download=1` | session cookie (or bearer); re-checks permissions; streams with `CSP: sandbox allow-scripts` |
| POST | `/api/v1/artifacts/{id}/rollback` | `{"seq": n}`; history is kept |
| POST/DELETE | `/api/v1/artifacts/{id}/grants[/{userID}]` | owner only |
| DELETE | `/api/v1/artifacts/{id}` | soft delete |
| GET | `/api/v1/users?email=` | account lookup for the share dialog |
| POST | `/mcp` | MCP, agent-only |
| POST | `/mcp/blobs` | agent-only: accepts a body out of band, answers a short-lived `upload_id` |
| GET | `/mcp/artifacts/{id}/versions/{seq}/raw` | agent-only content read, for a download that should land in a workspace |

Every user route accepts either credential:

- the platform's RS256 JWT (`Authorization: Bearer …`, validated against
  nanoauth's JWKS), or
- the `nw_artifacts` session cookie the SPA mints from that JWT.

The cookie is what makes a **new tab** work. Ctrl+click, middle-click and a
pasted link all land on this app with no parent frame to hand over a token, so
the page asks `GET /api/v1/session` who it is and carries on. Cookie-
authenticated writes additionally require the request to be same-origin
(`Sec-Fetch-Site` / `Origin`); cookie minting itself stays bearer-only so a
cookie cannot renew forever.

Content is read through the same cookie on `/versions/{seq}/raw`, and the
permission check runs on every load: revoking a share takes effect immediately,
and a preview open for hours does not turn into a 403. MCP requests carry the shared internal token
**and** `X-Nanowork-Agent-ID`; the header is what maps a call to its owner.

## MCP tools

`artifact_publish` · `artifact_list` · `space_list` · `tree_get` · `artifact_get` ·
`artifact_rename` · `artifact_grant` · `artifact_delete`

Registered in the catalogue by `nanoadmin mcp seed` as `scope=platform`, opt-in
per agent type (a new server must not silently widen what an agent can do). The
seeder matches an existing platform row **by endpoint as well as by id**: a
deployment that configured this server by hand under another name keeps its row,
and a deploy does not add a second copy of the same endpoint.

### Publishing a document without putting it in the conversation

JSON-RPC carries JSON values, so the original shape — the whole HTML document as
the `html` argument — made the model write the page twice: once as output, then
again on every later turn that replayed the conversation. A large page also runs
into the provider's output limit, which truncates the tool call.

Publishing therefore has a byte path beside the call:

1. the agent writes the page to a file in its workspace (`write`, `edit`);
2. it calls `artifact_publish(html_file: "artifacts/report.html", …)`;
3. the runtime uploads the file to `POST /mcp/blobs` and substitutes the handle
   it gets back (`nanoartifacts://upload/<id>`) for the path, so the call the
   server sees carries a URI, not a document;
4. `artifact_publish` claims the handle — once, within 15 minutes, only for the
   owner who uploaded it — and publishes the blob it already has.

The tool schema declares this with `x-mcp-file` on `html_file`
(`{"transferModes":["upload"],"uploadEndpoint":"/mcp/blobs"}`). The declaration is an instruction to the client,
not the model: `nanosec` strips it before the schema reaches the provider and
honours it in its MCP bridge. A client that does not understand it can still
publish with the deprecated inline `html` argument.

Reading follows the same rule in reverse: `artifact_get(download_to: "artifacts/report.html")`
answers with a descriptor (`raw_path`, `size_bytes`, `content_hash`) instead of
the body; the runtime fetches `GET /mcp/artifacts/{id}/versions/{seq}/raw`,
verifies the hash and writes the file. `include_html` still inlines a small body.

## Commands

```sh
nanoartifacts serve          # HTTP + MCP + the built SPA
nanoartifacts migrate up     # apply the embedded manifests
nanoartifacts gc             # orphan chunks + unreferenced bodies
nanoartifacts version
```

## Environment

| Variable | Required | Meaning |
|---|---|---|
| `NANOARTIFACTS_MONGO_URI` | yes | its own database; the URI must name it |
| `NANOARTIFACTS_SESSION_SECRET` | yes | HMAC key for the preview-session cookie |
| `NANOSEC_PORTAL_URL` | yes | identity lookups: the portal owns the agent rows |
| `NANOAUTH_INTERNAL_URL` | yes | identity lookups: nanoauth owns the accounts |
| `NANOSEC_BUS_INTERNAL_TOKEN` | yes | shared internal token: MCP callers and those lookups |
| `NANOAUTH_JWKS_URL` | yes | token verification |
| `NANOARTIFACTS_QUOTA_BYTES` | no | default 2 GiB per user |
| `NANOARTIFACTS_WEB_DIR` | no | built SPA; default `web/dist` |
| `NANOARTIFACTS_ADDR` | no | default `:8080` |
| `NANOARTIFACTS_COOKIE_PATH` | no | preview-cookie scope; default `/` (tighten to `/nanoartifacts/api` if desired) |

## Development

```sh
go test ./...          # MongoDB-backed tests skip without NANOARTIFACTS_MONGO_URI
go vet ./...
cd web && npm test && npm run build
```

To run the storage tests against a real database, point
`NANOARTIFACTS_MONGO_URI` at a throwaway database; the tests create and drop
their own.
