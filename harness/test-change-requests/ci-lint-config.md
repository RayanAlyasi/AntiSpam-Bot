# Change request: add `.golangci.yml` and update `check.sh` for the new CI checks

**Status:** blocks the `lint` job (and so `make check (ubuntu-24.04)`) on `feat/ci-complete`.
Agents can't write `.golangci.yml` or `harness/scripts/` (`PROTECTED_WRITE` in
`harness/scripts/hooklib.py`), so a human has to add them.

## What's needed

1. **Add `.golangci.yml`** (below) at the repository root on `feat/ci-complete`. The new `lint`
   job fails on purpose while it's missing, so lint can't silently skip again.
2. **Apply the `check.sh` change** (below), so `make check-fast` matches CI: a `go mod tidy`
   step, and no go-arch-lint step (depguard in `.golangci.yml` now enforces the architecture
   rules; no `.go-arch-lint.yml` was ever added at the root). CLAUDE.md's `make check-fast` line
   could drop "architecture" the same way.
3. **Delete `harness/proposed/.golangci.yml` and `harness/proposed/.go-arch-lint.yml`** (the
   initializer's untracked proposals in the main checkout). This config supersedes both: it keeps
   all of their depguard rules. It leaves out their contextcheck, exhaustive, noctx, zerologlint
   and forbidigo rules, because those find issues in existing code that need real changes, not
   mechanical ones.
4. **Note one gitleaks allowlist entry.** gitleaks flags the placeholder `sk-live-SECRET123` at
   `acceptance/config/config_test.go:461`. It's a hidden test, so the judge read it, not me.
   `.gitleaks.toml` allows exactly that value at that path.

## Verified

With this config (`golangci-lint` v2.15.0, as pinned in CI), `golangci-lint run ./...` reports
0 issues on `feat/ci-complete`. A probe file in `internal/scoring` importing pgx and
whatsmeow got both depguard errors.

`.gitleaks.toml` and `.golangci.yml` decide what CI lets through, so they may belong in the
guard's protected list (or CODEOWNERS) too.

What I fixed in code instead of excluding: errcheck (healthcheck, replay and corpus output,
hash writes), errorlint (decode errors now wrap their cause with a second `%w`), nilerr in the
LID resolver, unconvert, gosec G116 (literal bidi characters in the blacklist normalizer, now
`\u` escapes), and gofumpt on eight files.

What the config excludes, and why, is in its comments. Briefly:
- **gosec G115** everywhere: hash bit patterns stored as bigint and bounded counts in int16/int32
  columns (25 findings, none an overflow).
- **G304 and G703** (file paths, the second by taint analysis) in `cmd/antispam` and
  `internal/config`: operator-supplied paths. **G404** in the executor (pacing jitter).
- **nilerr** in the fastlane executor loop and autopair: they return nil when the context ends.
  nilerr reports the `return nil` line, so a source-based rule can't single these out.
- **Tests:** gosec; errcheck on `.Close`; misspell in `internal/detect` tests (spam is
  misspelled on purpose); ST1018 (literal invisible characters).
- **Formatters:** four existing tests aren't gofumpt- or goimports-formatted, and agents can't
  edit them (listed under `formatters.exclusions.paths`). Run `golangci-lint fmt` on them, then
  delete those lines.

## `.golangci.yml`

```yaml
# golangci-lint v2. CI runs `golangci-lint run ./...` and fails on any issue; `make check-fast`
# reports only issues new since staging. Fix the code rather than excluding a finding, and give
# every exclusion below a reason. `//nolint` is rejected by the tamper audit.
version: "2"

run:
  # Exclusion paths below are relative to go.mod.
  relative-path-mode: gomod

linters:
  # standard: errcheck, govet, ineffassign, staticcheck, unused.
  default: standard
  enable:
    - bodyclose
    - copyloopvar
    - depguard
    - errorlint
    - gosec
    - misspell
    - nilerr
    - rowserrcheck
    - sqlclosecheck
    - unconvert
  settings:
    # The architecture rules from .claude/rules/go.md.
    depguard:
      rules:
        whatsmeow:
          # GO-004. Tests may build whatsmeow events to feed the adapter.
          files: ["$all", "!$test", "!**/internal/wa/adapter/**"]
          deny:
            - pkg: go.mau.fi/whatsmeow
              desc: only internal/wa/adapter imports whatsmeow; use the WAClient interface (GO-004)
            - pkg: go.mau.fi/libsignal
              desc: only internal/wa/adapter touches the WhatsApp protocol libraries (GO-004)
        domain:
          # GO-005.
          files:
            - "**/internal/normalize/**"
            - "**/internal/envelope/**"
            - "**/internal/detect/**"
            - "**/internal/scoring/**"
            - "**/internal/policy/**"
          deny:
            - pkg: github.com/jackc/pgx
              desc: domain packages take data and return decisions; storage stays behind interfaces (GO-005)
            - pkg: github.com/riverqueue/river
              desc: domain packages take data and return decisions; queues stay behind interfaces (GO-005)
            - pkg: database/sql
              desc: domain packages don't touch the database (GO-005)
            - pkg: net/http
              desc: domain packages do no network I/O; enrichment and AI calls sit behind interfaces
        fake-only-in-tests:
          files: ["$all", "!$test"]
          deny:
            - pkg: github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake
              desc: the fake WAClient is for tests only
    gosec:
      excludes:
        # Integer conversions: hash bit patterns stored in Postgres bigint, and bounded counts
        # in int16/int32 columns. The rule can't see the bounds, so it flags every one.
        - G115
  exclusions:
    paths:
      - internal/store/db # sqlc output
    rules:
      # The operator names these files on the command line or in CONFIG_FILE; nothing reads a
      # path that comes from WhatsApp.
      - path: ^(cmd/antispam|internal/config)/
        linters: [gosec]
        text: G304|G703
      # math/rand only spreads the action pacing; nothing secret comes from it.
      - path: ^internal/actions/executor\.go$
        linters: [gosec]
        text: G404
      # These loops return nil once their context is done: a clean shutdown, not a dropped
      # error. Every other error in them is logged with Err(err).
      - path: ^(internal/app/fastlane/fastlane|internal/wa/adapter/connector/autopair)\.go$
        linters: [nilerr]
      # Tests seed math/rand, open fixtures by path and write temp files.
      - path: _test\.go$
        linters: [gosec]
      # Closing a listener or a read-only fixture in a test can't lose data.
      - path: _test\.go$
        linters: [errcheck]
        text: \.Close` is not checked
      # Spam samples are misspelled on purpose ("bitcion").
      - path: ^internal/detect/.+_test\.go$
        linters: [misspell]
      # Obfuscation tests hold the invisible characters they must see through.
      - path: _test\.go$
        linters: [staticcheck]
        text: ST1018

formatters:
  enable:
    - gofumpt
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/RayanAlyasi/AntiSpam-Bot
  exclusions:
    paths:
      - internal/store/db
      # Existing tests agents can't edit; drop these once they're reformatted.
      - ^internal/detect/detect_test\.go$
      - ^internal/identity/tracker_community_test\.go$
      - ^internal/corpusimport/plan_test\.go$
      - ^acceptance/config/config_test\.go$

issues:
  max-issues-per-linter: 0
  max-same-issues: 0
```

## `harness/scripts/check.sh`

```diff
@@ -1,6 +1,7 @@
 #!/bin/bash
 # The gate. Usage: check.sh fast|full
-#   fast: build, vet, lint (new issues only), architecture, sqlc drift, race tests, tamper audit
+#   fast: go mod tidy, build, vet, lint (new issues only; includes the architecture rules), sqlc drift,
+#         race tests, tamper audit
 #   full: the same with all lint issues, plus integration tests (needs Docker)
 # Output stays terse (one line per step, first failures only); full logs go to .agent/logs/check-*.log.
 set -u
@@ -25,6 +26,7 @@
 skip() { printf 'skip  %s  (%s)\n' "$1" "$2"; }
 
 if [ -f go.mod ]; then
+  step tidy sh -c 'go mod tidy -diff && cd tools && go mod tidy -diff'
   step build go build ./...
   step vet go vet ./...
   if command -v golangci-lint >/dev/null 2>&1 && [ -f .golangci.yml ]; then
@@ -35,9 +37,6 @@
       step lint golangci-lint run ./...
     fi
   else skip lint "golangci-lint or .golangci.yml missing"; fi
-  if command -v go-arch-lint >/dev/null 2>&1 && [ -f .go-arch-lint.yml ]; then
-    step arch go-arch-lint check
-  else skip arch "go-arch-lint or .go-arch-lint.yml missing"; fi
   if [ -f sqlc.yaml ] || [ -f sqlc.yml ]; then
     step sqlc go tool -modfile=tools/go.mod sqlc diff
   else skip sqlc "no sqlc config"; fi
```
