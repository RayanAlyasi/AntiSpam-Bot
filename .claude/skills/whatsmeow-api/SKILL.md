---
name: whatsmeow-api
description: How to get correct facts about the whatsmeow, pgx, River, goose and sqlc APIs. Use before calling or changing any library API, especially whatsmeow, whose API changes often.
---
Library facts come from the installed version's source and docs, never from memory.

- Exact API: `go doc go.mau.fi/whatsmeow.Client.SendMessage`, `go doc -all go.mau.fi/whatsmeow/types/events`.
- Examples: `go doc -ex <pkg>`.
- The pinned version: `go list -m go.mau.fi/whatsmeow`.
- Navigation: use the Go language server (go-to-definition, references) when it's available.
- Source: `go env GOMODCACHE` gives the module cache. Read files there, read-only.

Verified facts and known gaps are in `docs/DESIGN.md` §2 (capability audit). Read the relevant table before relying on a capability. In particular:
- `UnwrapRaw()` doesn't unwrap every wrapper (§5.2);
- `parseBusinessProfile` doesn't return the business description or website (§2.2);
- join-request notifications arrive as raw nodes (§2.3).

If `go doc` and DESIGN.md disagree, `go doc` wins for *what the API is*. Note the difference in `harness/deviations/<ID>.md`.
