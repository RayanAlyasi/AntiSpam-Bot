# Complete and harden CI (v0.1.0)

Branch `feat/ci-complete`, PR into `staging`.

## Done

- `ci.yml`: parallel jobs, each with a timeout: `static checks` (adds `go mod tidy -diff` for
  both modules), new `lint`, new `security` (govulncheck through `tools/go.mod`, gitleaks over
  the PR's or push's commits), unit tests on amd64 and arm64, integration tests, new
  `workflow lint` (actionlint), `multi-arch image`, and the gate. `integration tests (arm64)`
  runs on pushes only, and the edge image waits for it. Required check names unchanged.
- The gate reads `needs` through `env:` (`toJSON(needs)` and jq) instead of `${{ }}` in `run:`.
- Hardening in all four workflows: top-level `permissions: {}` with per-job grants,
  `persist-credentials: false` except the whatsmeow bump (it pushes), `timeout-minutes`
  everywhere. go-arch-lint install removed; tools installed with `go install @version`.
- `.gitleaks.toml`: default rules; three allowlist entries, each by path AND value. Full
  history over all refs is clean with it.
- Lint fixes, mechanical: errcheck, errorlint, nilerr (LID resolver), unconvert, gosec G116,
  gofumpt. `tools/go.sum` tidied (70 missing lines).
- Docs: DESIGN.md §23.14 (new) and §23.9, RELEASING.md, CONTRIBUTING.md,
  AGENTIC_ENVIRONMENT.md §7.

## Blocked on a human

- `.golangci.yml` and `harness/scripts/check.sh` are protected:
  `harness/test-change-requests/ci-lint-config.md` holds both. The `lint` job fails until
  `.golangci.yml` is added.

## Verification

- `golangci-lint run ./...` (v2.15.0, proposed config): 0 issues. depguard probe: both rules fire.
- actionlint v1.7.12: clean (shellcheck not installed locally; the runner has it).
- govulncheck v1.9.0: no reachable vulnerabilities.
- gitleaks v8.30.1 `git --log-opts=--all`: no leaks with `.gitleaks.toml`, 6 without (all
  synthetic test values).
- Repo settings (read only): secret scanning and push protection are enabled.

## Follow-ups

- Dependabot security updates are disabled in the repo settings (version updates are on).
- `docs/AGENTIC_ENVIRONMENT.md` §5 and §10 still mention `.go-arch-lint.yml` and installing
  go-arch-lint.
