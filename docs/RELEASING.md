# Releasing

The maintainer's guide to the release automation. The design is
[DESIGN.md §23](DESIGN.md#23-releases-and-versioning); deployers want [VERSIONS.md](VERSIONS.md).

## What runs where

| Workflow | Trigger | Does |
|---|---|---|
| `ci.yml` | every PR; pushes to `main` and `staging` | static checks, lint, security scans, unit and integration tests, workflow lint and the multi-arch image build (below); on `staging` pushes it also publishes `:edge` and `:sha-<7>` once every job has passed |
| `pr-title.yml` | PRs into `staging` | fails a title that isn't a Conventional Commit (types `feat`, `fix`, `perf`, `docs`, `refactor`, `test`, `ci`, `build`, `chore`, `revert`; lowercase subject) |
| `release.yml` | pushes to `main` and `release/*` | release-please, then GoReleaser, then the back-merge into `staging` (below) |
| `whatsmeow-bump.yml` | Mondays, or by hand | opens or updates a PR into `staging` that bumps whatsmeow to its latest commit |
| Dependabot (`dependabot.yml`) | weekly | grouped PRs into `staging` for Go modules, actions, Dockerfiles and compose files |

### CI checks

`ci.yml` runs its checks as parallel jobs ([DESIGN.md §23.14](DESIGN.md#2314-continuous-integration)):
`static checks` (tidy, build, vet, sqlc drift, tamper audit), `lint` (golangci-lint with
`.golangci.yml`, including the architecture rules), `security` (govulncheck and gitleaks over the
new commits), unit tests on amd64 and arm64, integration tests, `workflow lint` (actionlint) and
the multi-arch image build. On pushes, the integration tests also run on arm64.

The branch rules require only `make check (ubuntu-24.04)`, `make check (ubuntu-24.04-arm)` and
`multi-arch image`. The first is a gate job that passes only when every amd64 job passed, so a new
job goes into its `needs` list, and the required checks stay the same.

GitHub's own secret scanning and push protection are on for the repository as well. To allow a
synthetic test value that gitleaks flags, add a narrow entry (path and value) to `.gitleaks.toml`.

### The release workflow

1. **`rp` (release-please)** runs on every push. It keeps a release PR into the branch open, which
   holds only `CHANGELOG.md` and `.release-please-manifest.json`. When that PR has just been merged,
   it creates the `vX.Y.Z` tag and a **draft** release with the changelog as its body.
2. **`goreleaser`** runs only after a release was created. It first checks that the tag really exists,
   is SemVer and points at a commit on the branch (release-please hides a tag-creation failure). Then
   it builds the binaries and images, signs them with cosign, writes SBOMs, uploads everything to the
   draft, **publishes** it, and attests the archives and the image with `actions/attest`.
3. **`back-merge`** creates `sync/<branch>-<tag>` (e.g. `sync/main-v0.1.0`) from the released commit
   and opens a PR from it into `staging`. If it conflicts with `staging`, it leaves a comment; resolve
   the conflict on that branch.

Immutable Releases are on, so a published release can't change. That's why assets go to a draft first
and the release is published last.

Configuration lives in `release-please-config.json`, `.release-please-manifest.json` and
`.goreleaser.yaml` (with `goreleaser.Dockerfile` for the release image).

## One-time setup

These need a repository admin. Until they're done, `release.yml` and `whatsmeow-bump.yml` fail at
their first step (creating the App token); nothing else is affected.

1. **Create the release GitHub App** (Settings → Developer settings → GitHub Apps → New):
   - repository permissions: **Contents**, **Pull requests** and **Issues**: read and write;
   - no webhook;
   - install it on **this repository only**;
   - generate a private key.
2. **Add the App as the only bypass actor on the `v*` tag ruleset** (Settings → Rules → Rulesets).
   Don't give `GITHUB_TOKEN` or GitHub Actions a bypass: that would let any workflow on any branch
   create release tags.
3. **Create the `release` Environment** (Settings → Environments):
   - deployment branches: `main` and `release/*` only;
   - environment variable **`RELEASE_APP_CLIENT_ID`**: the App's client ID;
   - environment secret **`RELEASE_APP_PRIVATE_KEY`**: the App's private key (the whole `.pem`).

   Create it **before** the first run of `release.yml` or `whatsmeow-bump.yml`. If a workflow names
   an environment that doesn't exist, GitHub creates it automatically **with no branch
   restriction**; if that happens, add the restriction straight away.
4. **Add `release/*` to the branch ruleset** (PR-only, like `main` and `staging`).
5. **Keep Immutable Releases on** (Settings → General → Releases). The workflow depends on it: it
   uploads to a draft and publishes last.
6. **Turn on private vulnerability reporting** (in the repository's security settings), which
   [SECURITY.md](../SECURITY.md) points reporters to.
7. **Check the package settings** after the first image push: the `antispam-bot` package on ghcr.io
   should be public and linked to this repository.

If the back-merge or the whatsmeow bump fails with a "workflows" permission error, also give the App
**Workflows: read and write**: GitHub can refuse a ref an App creates when its commits change files
under `.github/workflows/`.

## Normal release

1. Open a **release PR from `staging` into `main`** and merge it with a **merge commit** (never
   squash), so `main` gets the exact commits tested on staging.
2. release-please opens or updates **its own release PR** into `main`. Review the generated
   `CHANGELOG.md`, especially the "⚠ BREAKING CHANGES" section, whose upgrade steps come from
   `BREAKING CHANGE:` footers.
3. **Merge release-please's PR** with a merge commit. There's no manual tagging: the workflow creates
   the tag and the draft, and GoReleaser fills and publishes it.
4. **Verify** the published release (commands in [VERSIONS.md](VERSIONS.md#verifying-a-release)):
   - the image runs on amd64 and arm64;
   - `cosign verify` passes on the image and the checksums;
   - `gh attestation verify` passes;
   - the SBOMs are present;
   - the `X.Y` and `latest` tags moved (for a stable release).
5. **Merge the back-merge PR** (`sync/main-<tag>` → `staging`) with a merge commit.

Visible `docs` commits trigger patch releases, so let docs changes batch up on `staging` rather than
releasing for each one.

**Release candidates:** before step 1, test the `sha-<7>` (or `edge`) image of the staging commit
you're about to release, in shadow mode first for a minor release.

## Hotfix

1. Fix it on `staging` first.
2. If `staging` is shippable, do a normal release.
3. If not:
   - create `release/X.Y` from the last release tag;
   - cherry-pick the fix into it by PR;
   - release-please's release PR on `release/X.Y` produces `vX.Y.(Z+1)`; merge it;
   - verify, then merge the back-merge PR (`sync/release-X.Y-<tag>` → `staging`).

For a WhatsApp protocol break (`405 client outdated`), this is the same-day route: merge the
whatsmeow bump PR (retitled `fix: …` so it counts as a fix), then release a patch.

## To check on the first release

DESIGN.md §23.13 lists what couldn't be verified before the first real release, such as whether
pre-releases leave `X.Y` and `latest` alone, whether `docker_signs` covers `dockers_v2` images,
and whether the tag ruleset honours the App's bypass.
