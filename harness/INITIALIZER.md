# Initializer session prompt

Run this once, in a fresh session, after the prerequisites in `docs/AGENTIC_ENVIRONMENT.md` §11 are installed:
`claude "$(cat harness/INITIALIZER.md)"` (or paste everything below the line).

---

You are setting up the Go project skeleton that every later feature session builds on. You build no product features. Read `docs/DESIGN.md` §4.1 (stack), §18 (testing), §19 (deployment) and §20 (roadmap), and `docs/AGENTIC_ENVIRONMENT.md` §6.1 and §7.

Create, on a branch `feat/F-000-skeleton`:

1. **Module:** `go.mod` with `go 1.26.0` and `toolchain go1.27.2` (matching whatsmeow). Add a module path; ask the user for it if `git remote` doesn't make it obvious.
2. **Layout:** `cmd/antispam/main.go` (a minimal main that loads config and exits cleanly) and `internal/` with empty packages for the modules in DESIGN.md §4:
   - `wa`, `inbox`, `normalize`, `envelope`, `detect`, `scoring`, `policy`, `actions`;
   - `store` (sqlc output in `store/db`), `ai`, `identity`, `review`, `api`, `config`, `clock`.
   
   Each package gets a `doc.go` describing its responsibility in two or three lines.
3. **`internal/wa`:** the `WAClient` interface covering the operations in DESIGN.md §2.3 and §10 (send, revoke, edit, update participants, group info, user info, profile picture, newsletter info, invite-link info, set announce/locked/join-approval), plus `internal/wa/fake`: a fake client that records every call and can inject errors (rate limit 429, temporary ban, not admin, timeout). No whatsmeow import yet except in a placeholder adapter file.
4. **`internal/clock`:** a `Clock` interface (real and fake) so time-based code is testable with `testing/synctest`.
5. **Tooling** (needs the human to apply it; agents can't write these paths):
   - `.golangci.yml` (v2 format: standard linters plus bodyclose, contextcheck, depguard, errorlint, exhaustive, forbidigo, nilerr, noctx, rowserrcheck, sqlclosecheck, zerologlint; gofumpt and goimports formatters; depguard rules so only `internal/wa` imports whatsmeow and the domain packages don't import pgx or River);
   - `.go-arch-lint.yml` (deny-by-default component map matching the layout).
   
   Write both to `harness/proposed/` and tell the human to move them into place.
6. **`tools/go.mod`** pinning sqlc, goose and govulncheck as `tool` directives.
7. **`docker-compose.test.yml`** with `postgres:18-alpine` (`TZ=UTC`).
8. **CI** in `.github/workflows/ci.yml`:
   - `actions/setup-go` with `go-version-file: go.mod`;
   - `make check`;
   - a matrix of `ubuntu-24.04` and `ubuntu-24.04-arm`;
   - multi-arch Docker build with `CGO_ENABLED=0`.
9. **`harness/features.json`:** the build plan. Break every phase of DESIGN.md §20 into features small enough for one session each (roughly half a day of human work; expect 150–250). Each entry:
   ```json
   {"id": "F-001", "phase": "1a", "title": "...", "category": "detection|queue|storage|identity|ai|web|infra|...",
    "core": true, "design_refs": ["§7.2"], "depends_on": ["F-000"],
    "description": "one or two sentences of observable behaviour",
    "passes": false, "evidence": []}
   ```
   - Order by dependency.
   - `core: true` for the action queue, identity, scoring, enforcement and network bans.
   - Phase 0 (the spike with a real number) is a single human-run feature, `F-000a`.
10. **Check:** `make check-fast` must pass on the skeleton. Then commit. This skeleton commit needs a judge PASS like everything else, so run the judge agent first. Push the branch and open the PR with `gh pr create --base staging`, following CLAUDE.md's commit and PR style.

Report: the module path, the package list, the feature count per phase, and the two config files waiting in `harness/proposed/`.
