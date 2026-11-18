# nanoartifacts

The work library: agents publish self-contained HTML documents, and people browse,
share and version them.

It is one of the eight first-class services. It owns the `artifacts_` prefix, its
own front end, and an MCP endpoint of its own.

## What it is

- **Publish from a conversation.** An agent writes a report, a dashboard or a small
  app and publishes it over MCP without pasting the document into the chat.
- **Spaces and a tree.** Folders, documents and apps in a hierarchy, with access
  granted per space or per artifact.
- **Versions that do not grow.** Identical bytes are stored once and charged once;
  publishing the same page twice costs one copy.
- **Two surfaces, one code base.** A full page at `/artifacts` and two panels inside
  the console's workbench.
- **Anything the browser can render.** Each version is served in a sandboxed frame
  with a preview cookie, so a link keeps working in a new tab.

## Where it sits

| | |
|---|---|
| Collections | `artifacts_*`, plus the root collection `artifacts`; bodies in `artifacts_fs.files` / `.chunks` |
| Port | `8080` in the network; reached through the console's nginx at `/nanoartifacts/` |
| Paths | `/artifacts` (the shared link, served by the portal) and `/nanoartifacts/` (this service) |
| Upstream | agents (MCP), browsers (JWT or the preview cookie) |
| Reads from | `nanoauth` (accounts) and `nanoportal` (who owns an agent) — never their collections |

## Quick start

```bash
go build ./cmd/nanoartifacts
NANOARTIFACTS_MONGO_URI="mongodb://localhost:27017/nanosec" \
NANOARTIFACTS_SESSION_SECRET="$(openssl rand -hex 32)" \
NANOAUTH_JWKS_URL="http://localhost:14401/auth/v1/.well-known/jwks" \
NANOSEC_PORTAL_URL="http://localhost:14403" \
NANOAUTH_INTERNAL_URL="http://localhost:14401" \
NANOSEC_BUS_INTERNAL_TOKEN="…" \
  ./nanoartifacts serve

./nanoartifacts migrate up   # the embedded manifests
```

## Configuration

| Variable | Required | Meaning |
|---|---|---|
| `NANOARTIFACTS_MONGO_URI` | yes | the platform database; the URI must name it |
| `NANOARTIFACTS_SESSION_SECRET` | yes | HMAC key for the preview cookie |
| `NANOSEC_PORTAL_URL` | yes | agent ownership |
| `NANOAUTH_INTERNAL_URL` | yes | account lookups for the share dialog |
| `NANOAUTH_JWKS_URL` | yes | token verification |
| `NANOSEC_BUS_INTERNAL_TOKEN` | yes | MCP callers and the two lookups above |
| `NANOARTIFACTS_QUOTA_BYTES` | no | per-user ceiling, default 2 GiB |
| `NANOARTIFACTS_WEB_DIR`, `NANOARTIFACTS_ADDR`, `NANOARTIFACTS_COOKIE_PATH` | no | built SPA, listen address, cookie scope |

## HTTP

| Method | Path | Notes |
|---|---|---|
| GET | `/healthz` | liveness |
| GET | `/api/v1/artifacts?scope=owned\|shared&q=&kind=` | the caller's JWT decides visibility |
| POST | `/api/v1/artifacts` | publish (`html`, `title`, `kind`, optional `artifact_id`) |
| GET | `/api/v1/artifacts/{id}` | metadata + versions + grants |
| POST | `/api/v1/session` | exchange the JWT for the preview cookie |
| GET | `/api/v1/artifacts/{id}/versions/{seq}/raw?download=1` | cookie or bearer; re-checks permission on every load |
| POST | `/api/v1/artifacts/{id}/rollback` | `{"seq": n}`; history is kept |
| POST/DELETE | `/api/v1/artifacts/{id}/grants[/{userID}]` | owner only |
| DELETE | `/api/v1/artifacts/{id}` | soft delete |
| POST | `/mcp`, `/mcp/blobs` | agent-only: publish inline or by handle |

## MCP tools

`artifact_publish` · `artifact_list` · `space_list` · `tree_get` · `artifact_get` ·
`artifact_rename` · `artifact_grant` · `artifact_delete` ·
`folder_create` · `node_move` · `node_copy` · `node_delete`

`artifact_delete` removes the work and every tree row that shows it. `node_delete`
removes one row and refuses a folder that still holds nodes; a document's work goes
with it when no other row shows it and the caller owns it.

Seeded by `nanoadmin mcp seed` as `scope=platform`, opt-in per agent type — a new
server must not silently widen what an agent can do.

**Publishing a large page.** JSON-RPC would make the model write the document twice:
once as output, then again on every turn that replays the conversation. So the tool
takes a path: the agent writes the file in its workspace, calls
`artifact_publish(html_file: "artifacts/report.html")`, the runtime uploads it to
`POST /mcp/blobs` and substitutes a handle the server claims once, within 15 minutes,
for that owner only. Reading follows the same rule in reverse:
`artifact_get(download_to: …)` answers with a descriptor instead of the body.

## Design decisions

- **The id is the content's sha256.** Publishing identical bytes stores and charges
  one copy; the version row is metadata, the body is shared.
- **Blobs are written before the version row.** A crash leaves an orphan chunk that
  `nanoartifacts gc` removes — never a version pointing at nothing.
- **Delete is soft.** A row is marked, the body survives until the garbage collector
  proves nothing references it.
- **Two prefixes on purpose.** The page is `/artifacts` (portal), the app and its API
  are `/nanoartifacts/` (this service). Trailing-slash normalisation in a proxy used
  to send a portal URL into the service, which opened a new tab without the console.
- **The preview cookie is what makes a link work.** Ctrl+click and pasted links have
  no parent frame to hand over a token, so the page asks the session endpoint who it
  is. Cookie minting stays bearer-only, so a cookie cannot renew itself forever.
- **A panel is not a page.** The workbench hosts two panels from the same build
  (federation remote, React taken from the host); the panel shows one view at a time,
  because a narrow column is not the place for a three-column layout.

## Docs

- [`docs/ARCHITECTURE.md`](https://gitee.com/kobeyoung81/nanosec/blob/master/docs/ARCHITECTURE.md) — the platform around this module
- [`docs/DEPLOYMENT.md`](https://gitee.com/kobeyoung81/nanosec/blob/master/docs/DEPLOYMENT.md) — ports, the nginx mounts, operations
- [`docs/README.md`](https://gitee.com/kobeyoung81/nanosec/blob/master/docs/README.md) — every document

## Status

Released as part of `v0.1.0`; the version of this module is pinned in the
super-repo's `versions.lock`.

## Contributing and security

[`CONTRIBUTING.md`](https://gitee.com/kobeyoung81/nanosec/blob/master/CONTRIBUTING.md)
for setup and commit rules; report vulnerabilities privately through
[`SECURITY.md`](https://gitee.com/kobeyoung81/nanosec/blob/master/SECURITY.md), never
in a public issue.

## License

[AGPL-3.0](./LICENSE). The name and logo are covered separately — see
[`NOTICE`](./NOTICE).
