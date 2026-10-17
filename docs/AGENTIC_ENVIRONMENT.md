# Agentic Build Environment: Design

**Status:** Draft for review (revision 3: changed after a second research pass against Anthropic's articles and model docs, and a mechanism-by-mechanism check against the Claude Code docs; see §14). Nothing is set up yet.
**Purpose:** The Claude Code environment that builds the anti-spam bot described in [DESIGN.md](DESIGN.md). It covers the agents, models, instructions, skills, hooks, checks, state files, the scoring of every output, and the self-improvement loop.

---

## 1. The system at a glance

```
You ─▶ /next-feature F-xxx
          │
          ▼
   Architect (Opus 5.5, high; read-only subagent) ── behaviour-level contract + hidden acceptance tests
          │                                         (public API only, from DESIGN.md)
          ▼
   You approve the contract    (first ~10 features; afterwards only core or unclear features)
          │
          ▼
   Builder (Opus 5.5, medium; high for core packages) ── branch feat/F-xxx, .agent/plan.md checklist
          ├─▶ Explore (Haiku 5.5, medium) ...... only for broad searches; direct grep/gopls otherwise
          ├─▶ /whatsmeow-api skill ............. `go doc` + gopls in the builder's own context
          ├─▶ Advisor (opt-in, measured) ....... approach / repeated error; dropped if no gain
          └─▶ make check ....................... build, vet, lint, architecture, race tests,
                    │                            sqlc drift, tamper audit, acceptance tests
                    │ green
                    ▼
   Judge (Opus 5.5, high; fresh context) ── reports everything with severity + evidence (or UNKNOWN);
          │                                 blocks only on correctness / requirement gaps;
          │                                 core packages: also attacks (evasion, wrongly flagged
          │                                 members, pacing), backing claims with failing tests
          │ PASS → a hook stamps the diff hash
          ▼
   commit (Claude hook + git pre-commit both verify stamp) ─▶ push feat/F-xxx ─▶ gh pr create ─▶ CI ─▶ YOU merge
          │
          ▼
   Scorer (script) ─▶ scorecard ─▶ trend report         Retro (Sonnet 5.5) ─▶ incidents, dead ends, proposals
   after ~20–30 features: Curator (Opus 5.5) ─▶ rules diff ─▶ Harness Eval (replay A/B) ─▶ you approve
```

**What changed from revision 2, and why** (sources in §14):
- **The Builder moves from Sonnet 5.5 to Opus 5.5.** Anthropic's own measurements, on their published coding benchmark:

  | Model | Solved | Cost per solved task |
  |---|---|---|
  | Sonnet 5.5 at medium | about 7 points lower than at high | — |
  | Sonnet 5.5 at high | 87.6% | $0.11 |
  | Opus 5.5 at medium | 92.8% | $0.22 |

  Anthropic recommends starting agent work on Opus 5.5 at its default effort, `medium`. Sonnet at low/medium is also "more likely to stop and check in", which is bad for unattended runs. `opusplan` is no longer needed.
- **The Architect becomes a read-only subagent, and it also writes the acceptance tests** (Test Author merged in). Anthropic found that over-detailed upfront plans make errors cascade. Contracts therefore describe *behaviour and public APIs*, not internal types, and hidden tests target only public behaviour.
- **Scout and Librarian are removed as agents.** Anthropic's docs name "spawning a subagent where a grep would do" as a known overuse. Library lookups at Haiku-low would be lossy for exactly the facts most likely to cause bugs, so the builder runs `go doc` itself. Explore on Haiku is kept for wide searches only.
- **The Adversary is folded into the Judge** for core packages. One reviewer that both reviews and attacks avoids stacking three model-based checks, which Anthropic's Claude 5 guidance warns against.
- **The Judge is rebuilt:** it reports everything with evidence, has an `UNKNOWN` option, blocks only on real problems, re-reviews only the changed lines, and never repeats a finding. A reviewer told to "assume it's wrong" always finds something, and chasing every finding leads to over-engineering.
- **The Advisor becomes opt-in and measured.** On current models, Anthropic measured +1.7 points at 2.1× cost, "at the edge of run-to-run noise". It's experimental, and it costs about 1k tokens per request even when unused.
- **Curation and harness A/B tests wait until there's enough data** (~20–30 features). With 10–20 eval tasks, a small rule change can't be told apart from noise.

---

## 2. Principles

| # | Principle | Evidence |
|---|---|---|
| P1 | **Deterministic checks are the main lever.** One command decides "done", and hooks enforce it. | Anthropic's C-compiler and long-running-harness work. CLAUDE.md is advisory; hooks are enforced. |
| P2 | **Keep instructions lean.** CLAUDE.md is a map plus gotchas; rules are scoped to file paths; procedures live in skills; instructions are added only after real failures, with the reason stated. | Claude 5 context-engineering post (over 80% of Claude Code's system prompt removed with no loss); the ETH study on repo instruction files. |
| P3 | **One feature per session, fresh context, state on disk (including dead ends).** | Anthropic's long-running-agent harness; "successive sessions will re-attempt the same dead ends" (long-running Claude research). |
| P4 | **Start simple, add parts only when measured.** Every part of the harness must earn its place on the scorecard. | Building effective agents ("find the simplest solution possible"); harness design (remove one component at a time). |
| P5 | **Make cheating impossible, and give a legitimate exit.** | ImpossibleBench; Darwin Gödel Machine. |
| P6 | **Measure every output; improve the harness in small, tested steps, once there's enough data.** | Anthropic evals guide (20–50 tasks from real failures, read transcripts); ACE; Meta-Harness. |
| P7 | **Every part is provisional.** Re-run the ablation when the model changes. | Harness design (retired sprints and resets). |

---

## 3. Agents

| Agent | Model / effort | Job | Tools | Output |
|---|---|---|---|---|
| **Architect** | Opus 5.5, high | From `features.json` + DESIGN.md: a one-page **behaviour-level contract** (what it must do, edge cases, public API names, which DESIGN.md sections apply, out-of-scope) **and** black-box acceptance tests in `acceptance/` against the public API. Internal design is left to the builder. | Read, Grep, Glob; Write `.agent/contracts/**`, `acceptance/**`; Bash(`go test`, `go vet`, `go doc`) | contract + acceptance tests whose names and failure messages say exactly what was expected |
| **Builder** | Opus 5.5, medium; **high** for the queue, identity, scoring and network bans | Implements on `feat/F-xxx` and writes its own unit, table, fuzz and property tests. Keeps a checklist in `.agent/plan.md`. Iterates until `make check` is green. Doesn't delegate work it can finish in a few tool calls, and never delegates verification of its own work. | full edit; Bash per permissions; Explore | code + tests |
| **Explore** (built-in, model overridden) | Haiku 5.5, medium | Only for wide, many-file searches. Single lookups use grep or the gopls plugin directly. | read-only | `path:line — ≤ 8 words` per hit |
| **Advisor** (built-in, optional) | Fable 5.1 or Opus 5.5 (must rank at or above the builder) | Approach choices and repeated errors. Not "am I done?", which is the Judge's job. | built-in | — |
| **Judge** | Opus 5.5, high, fresh context | Reviews the diff against the contract, the DESIGN.md sections and the rules. **Reports everything** as `file:line — severity (blocker/major/minor/nit) — evidence — fix`, or `UNKNOWN` when it can't tell. **Blocks only on correctness and requirement gaps.** For core packages it also attacks: spam that evades, members wrongly flagged, Unicode tricks, pacing and deadline edges. Claims are backed by a failing test in `.agent/scratch/` where possible. On re-review it reads only the new diff and never repeats a resolved finding. Finding nothing is a valid outcome. | Read, Grep, Glob; Write `.agent/scratch/**`; Bash(read-only git, `go test`, `go vet`) | first line `PASS` / `FAIL` / `UNKNOWN`, then findings |
| **Retro** | Sonnet 5.5, medium | After each feature: writes incidents, **dead ends** ("tried X, failed because Y") and proposed lessons. It can't edit rules, skills or CLAUDE.md. | Read, Grep; Write `harness/incidents/**`, `harness/proposals/**`, `harness/dead-ends.md` | files |
| **Curator** | Opus 5.5, high (you run it) | From feature ~20–30 onward: merges proposals into rules as a **diff** for you to approve; prunes using the counters. | Read; Write `harness/curation/**` | patch + rationale |

**Limits on delegation:**
- `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH=2`.
- The builder's prompt includes Anthropic's tested "don't over-delegate" paragraph (Opus 5 guidance).

---

## 4. Instructions

### 4.1 CLAUDE.md (≤ 100 lines)

What it contains:
- what the repo is, with DESIGN.md as a **plain path** (an `@import` loads the whole file every session);
- the commands;
- the routine (`/next-feature`);
- the definition of done;
- hard boundaries, each with its reason;
- Anthropic's tested scope-control text: surgical edits; pre-existing bugs go in `progress.md` as follow-ups; tests sized like their neighbours; general solutions, never special-cased for tests.

What it leaves out:
- anything the code already shows;
- style rules (linters enforce them);
- "think carefully" / "double-check" / CRITICAL wording.

It's audited with `/doctor` regularly.

### 4.2 Rules = the playbook (path-scoped)

`.claude/rules/{go,sql,whatsmeow,queue,detect,ai,tests}.md`. Each file loads only when matching files are touched.

**Bullets carry only an ID and the rule text:**

```
- [WA-007] Ack a whatsmeow event only after the inbox insert commits; handlers run on the receive loop.
```

Evidence and helpful/harmful counters live in a **sidecar file** (`harness/rules-ledger.json`) that is never loaded into context. That keeps the loaded tokens high-signal.

**Seed rules** come from DESIGN.md's verified gotchas. Each file is capped at about 40 bullets.

**Auto-memory is disabled for this project** (`autoMemoryEnabled: false`). Otherwise Claude would save its own memories and bypass the rule that only the Curator changes the playbook. Anything worth remembering goes through Retro → proposals.

### 4.3 Skills

| Skill | What it does |
|---|---|
| `/next-feature [F-xxx]` | the whole flow in §1, with checklists |
| `/add-migration` | goose file → `sqlc generate` → `make check-gen` |
| `/add-analyzer` | Analyzer interface + envelope fixtures + table and property tests |
| `/add-action` | new queue action: priority, dedupe key, deadline, fake-client test |
| `/whatsmeow-api` | look up the real API with `go doc`/gopls (never from memory); pinned version; known gaps from DESIGN.md §2 |
| `/test-change-request` | the legitimate exit: explain why a test or contract looks wrong, then stop |
| `/harness-report` | scorecard trends (§8) |
| `/harness-curate`, `/harness-eval` | enabled from feature ~20–30 (§9) |

---

## 5. Guardrails

### 5.1 Hooks

| Event | Hook | What it does |
|---|---|---|
| PostToolUse `Edit\|Write` | `format-go.sh` | `gofumpt` + `goimports` on edited `.go` files. |
| PreToolUse `Read\|Grep\|Glob\|Edit\|Write\|Bash` | `guard.sh` | Uses the hook's **`agent_type`** field (present inside subagents, empty in the main session):<br>• **main session (Builder):** blocks reading or writing `acceptance/**`, writing already-committed `*_test.go`, `testdata/fixtures/**`, `harness/evals/**`, `.agent/stamps/**`, `.claude/**`, lint and architecture configs, and the Makefile `check` targets;<br>• **Architect:** may write `acceptance/**` and contracts only;<br>• **Judge:** may read everything and write `.agent/scratch/**` only.<br><br>The block message points to `/test-change-request`. Bash is pattern-matched (`cat`, `sed -i`, redirects, `mv`, `rm`, `git checkout` on those paths). That's a guardrail, not a wall; the sandbox (§5.3) is the wall. |
| SubagentStop matcher `judge` | `stamp.sh` | If `last_assistant_message` starts with `PASS` and `make check` is green, the **hook** writes `.agent/stamps/<sha256 of staged diff>` and deletes older stamps. |
| PreToolUse `Bash` (no `if`; the script parses the command) | `commit-gate.sh` | Blocks any `git … commit` (including `git -C`, `git -c`, `sh -c`) without a stamp for the exact staged diff, and blocks `--no-verify`. |
| git `pre-commit` (in `.git/hooks`, shared by worktrees) | `pre-commit` | The same stamp check at the git level, so nothing slips past the Claude hook. |
| Stop | `stop-gate.sh` (timeout 900 s) | If Go files changed: `make check-fast`. Terse output: summary plus the first failures; full logs go to `.agent/logs/`. Has the `stop_hook_active` guard. **Also:** if `.agent/plan.md` still has open items and no blocker is written, it continues the turn (handles "ended with a progress report instead of finishing"), capped at 3 continuations. |
| SessionStart `startup\|compact` | `session-start.sh` | ~15-line state summary: feature, contract, gate status, open requests, recent dead ends. |
| SessionEnd (timeout 30 s) | `score.sh` | Writes the scorecard line. Cost comes from the run's JSON output (unattended) or the statusline cost feed (interactive), not from the transcript. |

**Tamper audit** (part of `make check`, and in CI). It fails on:
- removed assertions;
- new `t.Skip`;
- `//nolint` additions;
- lowered coverage floors;
- test-excluding build tags;
- test-fixture literals in non-test code.

### 5.2 Permissions

- **Allow:** `go`, `make`, `gofumpt`, `golangci-lint`, `sqlc`, `go-arch-lint`, `git status/diff/log/add/commit/switch -c`, `git push origin feat/*`, `gh pr create/view/checks`, `docker compose` for the test database.
- **Ask:** other pushes, `git reset --hard`, `git rebase`, other `docker` commands.
- **Deny:** `Read(./.env*)`, `rm -rf`, pushes to `main`, `gh pr merge`, anything that pairs or connects a real WhatsApp session.
- **Hidden tests** are enforced by the `agent_type` hook in §5.1, not by a permission rule. Permission rules apply to every agent, so they would lock out the Architect too.
- **No spending limit** (your decision). Stuck loops stop on caps (§6.2).

### 5.3 Unattended runs (Stage B)

- **Isolation:** each run happens inside a **container or the Claude Code sandbox** (writes limited to the repo and the Go cache; network limited to Go module hosts, GitHub and the AI API).
- **Folder trust:** the folder is trusted once, so project allow-rules apply in `-p` mode. Otherwise `-p` ignores them.
- **Permission mode:** `--permission-mode dontAsk` with explicit `--allowedTools`.
- **Never `--bare`:** it would skip the hooks, skills, agents and CLAUDE.md.
- **Completion check:** `/goal` or the Stop-hook checklist confirms the feature is actually complete.

### 5.4 Human-only

- Pairing a WhatsApp number, and anything that sends from a real account.
- Merging PRs.
- Production migrations.
- Approving contracts during Stage A, and afterwards for core or unclear features.
- Approving any change to CLAUDE.md, rules, skills, agents, hooks, checks or evals.
- Reading a sample of Judge transcripts each week to tune it. Anthropic: out of the box, Claude needs calibration as a QA agent.

---

## 6. The build loop

### 6.1 Once: the initializer session

- **Project skeleton:** `go.mod` (go 1.26 / toolchain 1.27.2), Makefile, `.golangci.yml`, `.go-arch-lint.yml` (deny-by-default module map), depguard, CI (arm64/amd64), docker-compose test Postgres, `tools/go.mod`.
- **Interfaces and fakes:** the `WAClient` interface with a fake client.
- **The plan:** `harness/features.json` from DESIGN.md's roadmap (ID, phase, dependencies, DESIGN.md sections, description, `core: true/false`, `passes`, `evidence`).
- **Git:** the git pre-commit hook, then the first commit.

### 6.2 Every feature (`/next-feature`)

1. **Orient:** read the git log, `progress.md`, `dead-ends.md` and `features.json`. `make check` must be green before new work.
2. **Pick:** the next feature whose dependencies pass. Create `feat/F-xxx`.
3. **Architect:** contract + acceptance tests. You approve the contract during Stage A, and later only for `core` or unclear features. Skip the contract entirely when the change fits in one sentence.
4. **Builder:** implements until `make check` is green.
5. **Judge:** `PASS` → stamped. `FAIL` → back to step 4 with blocker findings only. Re-review covers the new diff only.
6. **Commit** (both stamp checks) → push → `gh pr create` (contract + scorecard + Judge findings in the body) → CI.
7. **Record:** `passes: true` + evidence. Append to `progress.md`.
8. **Score + Retro.**
9. **You merge** (or request changes; recorded as a score input).

**Caps:**
- 3 Judge rounds (then a human looks);
- 2 identical gate failures in a row (then one Advisor consult if enabled, then stop);
- 150 turns.

Hitting a cap writes a blocker and a dead-end entry.

**The legitimate exit:** `/test-change-request`, then stop. Never edit the test.

### 6.3 Autonomy, in stages (your decisions)

| Stage | How | When |
|---|---|---|
| **A. Supervised + ablation** | You run `/next-feature` and approve contracts. The first features also run as an **ablation** (§8.3) to find out which agents actually earn their place. | First ~10 features |
| **B. Unattended, sequential** | `harness/run.sh` loops `claude -p "/next-feature"` in the sandbox (§5.3), one feature per fresh session. PRs queue up for you to merge. | Once the scorecard trend is stable |
| **C. Parallel** | 2–3 sessions via `claude --worktree`, only on independent packages, each with its own test-database schema. `-p` worktrees are cleaned up afterwards. | Later phases |

---

## 7. Testing (what `make check` runs)

- **No real WhatsApp:** everything goes through `WAClient` with a fake client (records actions; injects 429, temporary ban, not admin).
- **Spike fixtures:** messages captured in the spike are replayed as golden envelope tests (protected).
- **Time and pacing:** `testing/synctest` virtual time for the ceiling, slow-down, deadlines and coalescing.
- **Property tests** (`rapid`), for the DESIGN.md §18 invariants:
  - ask-admins mode never auto-bans;
  - priors never act alone;
  - a policy violation alone never bans;
  - established members are never auto-banned;
  - the rate never exceeds the ceiling.
- **Fuzzing:** normalization, URL and phone extraction, vCards, the unwrapper.
- **Postgres:** testcontainers, behind a build tag.
- **Architecture:** depguard + go-arch-lint.
- **Output:** `make check-fast` (affected packages) prints a summary plus the first failures, with full logs written to files.
- **Nightly:** gremlins mutation testing on core packages, govulncheck, full integration and acceptance runs.

---

## 8. Continuous evaluation: is it getting better or worse?

### 8.1 Scorecard

One JSON line per feature, tagged with the **harness version** (a hash of CLAUDE.md, `.claude/` and the agent prompts) and the **feature category**.

| Group | Metrics |
|---|---|
| Correctness | acceptance pass at the first gate run; Judge first verdict; blocker/major findings; CI; later bugs traced back to this feature |
| Process | gate runs until green; Judge rounds; caps hit; test-change requests (and whether each was valid); Stop-hook continuations; Advisor consults |
| Test quality | mutation score and coverage on touched packages; property and fuzz tests added |
| Integrity | tamper hits; guard blocks |
| Human verdict | merged as-is / after changes / rejected |
| Cost | $ and tokens (from `-p --output-format json`, or the statusline feed), wall time, turns |

A **composite score** with fixed, documented weights, led by correctness and your verdict.

### 8.2 Trend and alarms

- `/harness-report` shows rolling 10-feature windows per harness version and per category.
- **Alarms:**
  - composite score drops more than 10%;
  - any tamper hit;
  - Judge fail rate above 50% over 5 features;
  - an escaped bug.
  
  An alarm triggers the Retro and freezes harness changes until it's explained.
- **Monthly blind grade:** the Judge grades a sample of merged diffs with a fixed rubric (with `UNKNOWN` allowed), calibrated against your merge decisions.
- **Optional:** OpenTelemetry export for cost dashboards.

### 8.3 Ablation in Stage A (does each agent earn its place?)

The first 5 features are built **twice**, from the same starting commit:
- **(a) minimal:** Opus builder + `make check` + hooks + Judge;
- **(b) full:** + Architect contract, hidden tests, Advisor.

Compare the scorecards and keep only what measurably helps. Repeat whenever the model changes. Anthropic's harness work found some components stop paying off as models improve.

### 8.4 Eval set

`harness/evals/` grows from real incidents to **20–50 tasks** (Anthropic's recommendation). Each task is a clean commit + a feature prompt + hidden grading tests, split into regression, capability and held-out tasks. A human reads the transcripts of failed runs.

---

## 9. Self-improvement loop

```
every feature ─▶ Scorer + Retro ─▶ incidents, dead ends, proposals        (from day one)
                                         │
          from ~20–30 features: every ~5 features, on an alarm, or a rules file over its cap
                                         ▼
                             /harness-curate (Curator)
                 delta-merge into rules · counters in rules-ledger.json · prune harmful/unused
                 separate proposals for skill/agent/hook changes · output = DIFF, never a rewrite
                                         ▼
                             /harness-eval (A/B gate)
                 ONE change at a time · replay evals from clean worktrees, ≥ 3 runs per arm
                 accept only if: regression 100%, capability not lower, composite not lower, cost +15% max
                                         ▼
                 you approve ─▶ new harness version ─▶ tracked by scorecards ─▶ reverted if the next 10 score worse
```

**Guardrails:**
- Only the Curator changes the playbook, and only through a diff you approve.
- Agents never edit the checks, evals, scorer or hooks.
- Auto-memory is off.
- A new skill is added only after a pattern recurs 3 or more times and it passes the eval gate.

---

## 10. Repository layout

```
CLAUDE.md   Makefile   .golangci.yml   .go-arch-lint.yml
.claude/
  settings.json  (autoMemoryEnabled false, hooks, permissions, env, optional advisorModel)
  agents/  architect.md  judge.md  retro.md  curator.md  explore.md (Haiku override)
  skills/  next-feature/  add-migration/  add-analyzer/  add-action/  whatsmeow-api/
           test-change-request/  harness-report/  harness-curate/  harness-eval/
  rules/   go.md  sql.md  whatsmeow.md  queue.md  detect.md  ai.md  tests.md
  hooks/   format-go.sh  guard.sh  stamp.sh  commit-gate.sh  stop-gate.sh  session-start.sh  score.sh
.githooks/pre-commit
.agent/    plan.md  contracts/  stamps/  scratch/  logs/
harness/   features.json  progress.md  dead-ends.md  deviations.md  incidents/  proposals/  curation/
           test-change-requests/  evals/  rules-ledger.json  metrics.jsonl  CHANGELOG.md  run.sh
acceptance/   testdata/fixtures/
```

---

## 11. Before building (your machine)

| Need | Command (run by you) |
|---|---|
| Go 1.27.2 | `brew install go` |
| gopls (for the plugin) | `go install golang.org/x/tools/gopls@latest` |
| Lint, SQL, GitHub CLI | `brew install golangci-lint sqlc gh` then `gh auth login` |
| Architecture and mutation tools | `go install github.com/fe3dback/go-arch-lint@latest github.com/go-gremlins/gremlins/cmd/gremlins@latest` |
| Docker Desktop **running** | for real-Postgres tests |
| Go code intelligence | `claude plugin install gopls-lsp@anthropic-plugin-directory --scope user`. Use this plugin *instead of* the gopls MCP server, not both. |

## 12. Rollout

1. **You:** prerequisites (§11).
2. **Here:** build the harness, then **test every guardrail**:
   - the builder can't read `acceptance/`;
   - edits to protected tests are blocked;
   - a commit without a stamp is blocked, including via `git -C` and `--no-verify`;
   - a broken test is caught by the Stop gate;
   - an open checklist item continues the turn.
3. **Phase 0 spike, together** (fixtures captured).
4. **Stage A + ablation** (~10 features).
5. **Stage B, then C,** as the trend allows.

## 13. Decisions

| # | Decision | Status |
|---|---|---|
| 1 | Hidden acceptance tests | **Yes** (decided). Now written by the Architect, against public behaviour only. |
| 2 | Reviewer on a different model | **No** (decided) |
| 3 | Spending limit | **None** (decided); caps instead |
| 4 | Unattended after ~10 supervised features with a stable trend | **Yes** (decided) |
| 5 | Parallel worktrees later, 2–3 at once | **Yes** (decided) |
| 6 | Builder on Opus 5.5 (medium/high) instead of Sonnet 5.5 | **Yes** (decided) |
| 7 | Advisor off by default, tested in the ablation | **Yes** (decided) |

---

## 14. Sources

**Second-pass review (2026-10-17):**
- [Optimizing for cost and intelligence](https://platform.claude.com/docs/en/about-claude/models/optimizing-for-cost-and-intelligence): model and effort benchmarks, Advisor measurements, orchestrator-alone results
- Prompting pages for [Opus 5](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5), [Opus 5.5](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5-5), [Sonnet 5.5](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-sonnet-5-5) and [Haiku 5.5](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-haiku-5-5): verification stacking, review filtering, unattended stops, effort defaults
- [Claude Code best practices](https://code.claude.com/docs/en/best-practices): reviewer over-reporting, skipping plans for one-sentence diffs, `/sandbox`
- [Subagents in Claude Code](https://claude.com/blog/subagents-in-claude-code): "a handful of well-scoped agents"
- [How Warp builds self-improving agents](https://claude.com/blog/how-warp-builds-self-improving-agents-on-claude)
- [Long-running Claude](https://www.anthropic.com/research/long-running-Claude): dead ends
- Claude Code docs: [hooks](https://code.claude.com/docs/en/hooks) (`agent_type`, SubagentStop, `if` limits), [permissions](https://code.claude.com/docs/en/permissions) (rules apply to subagents; `-p` trust), [headless](https://code.claude.com/docs/en/headless), [advisor](https://code.claude.com/docs/en/advisor), [model config](https://code.claude.com/docs/en/model-config), [worktrees](https://code.claude.com/docs/en/worktrees), [goal](https://code.claude.com/docs/en/goal), [code intelligence plugins](https://code.claude.com/docs/en/plugins/code-intelligence)

**First pass:**
- Anthropic engineering: [long-running harnesses](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents), [harness design](https://www.anthropic.com/engineering/harness-design-long-running-apps), [evals](https://anthropic.com/engineering/demystifying-evals-for-ai-agents), [context engineering](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents), [tools](https://www.anthropic.com/engineering/writing-tools-for-agents), [skills](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills), [C compiler](https://www.anthropic.com/engineering/building-c-compiler), [multi-agent research](https://www.anthropic.com/engineering/multi-agent-research-system), [building effective agents](https://www.anthropic.com/engineering/building-effective-agents)
- Claude blog: [new rules of context engineering for Claude 5](https://claude.com/blog/the-new-rules-of-context-engineering-for-claude-5-generation-models), [Fable field guide](https://claude.com/blog/a-field-guide-to-claude-fable-finding-your-unknowns)
- Papers: ACE (2510.04618), Meta-Harness (2603.28052), SICA (2504.15228), Darwin Gödel Machine (2505.22954), Huxley-Gödel Machine (2510.21614), Evaluating AGENTS.md (2602.11988), SkillsBench (2602.12670), IFScale (2507.11538), memory management (2505.16067), ImpossibleBench (2510.20270), Agentic PBT (2510.09907)
- Practitioners: [Ralph](https://ghuntley.com/ralph/), [Böckeler on spec-driven development](https://www.martinfowler.com/articles/exploring-gen-ai/sdd-3-tools.html), [METR](https://metr.org/blog/2025-06-05-recent-reward-hacking)

---

## 15. As built (2026-10-16)

Implemented on branch `harness/agent-env`. Differences from the plan above:

- **Hooks** (`.claude/hooks/`):
  - **`guard.py`** is the single PreToolUse hook. It covers hidden tests, protected paths, per-agent write lists, git rules and the commit stamp check, so the separate `commit-gate.sh` was folded in.
  - **Redaction replaced by blocking:** instead of redacting search output, repo-wide searches by the builder are blocked. Grep and Glob need a `path`, and shell `grep -r`, `rg` and `find` are refused. The builder sees only test *results* from `acceptance/`.
  - **The rest:**
    - `stamp.py`: SubagentStop for `judge`;
    - `stop_gate.py`: gate failures capped at 5 continuations, open checklist items at 3;
    - `session_start.py`, `session_end.py`, `format-go.sh`.
- **Git lock:** `.githooks/pre-commit` re-checks the stamp. Turn it on once per clone with `make setup-hooks`. Humans commit their own work with `git commit --no-verify`.
- **Scripts** (`harness/scripts/`):
  - `hooklib.py`: the shared policy;
  - `check.sh`: the gate, with terse output and logs in `.agent/logs/`;
  - `tamper.py`, `scorecard.py`, `report.py`;
  - `run.sh`: Stage B;
  - `eval-run.sh`: harness A/B.
- **Agents:** `architect`, `judge`, `retro`, `curator` and `explorer` (Haiku). The built-in Explore is denied so `explorer` is used; `explorer` can't read `acceptance/` either.
- **Tested:** 48 guard cases and 20 flow cases (stamp → commit, pre-commit lock, tamper audit, Stop gate, session summary, scorecard, report) all pass, in a throwaway copy of the repo.
- **Not yet verified** (needs Go, the `claude` CLI on PATH, and real features):
  - `run.sh` and `eval-run.sh`;
  - Go steps of `check.sh` (they skip until `go.mod` exists);
  - `format-go.sh`;
  - mutation score and coverage in the scorecard (left `null` until core packages exist).
- **Known limit:** Bash parsing is a guardrail, not a wall. For example, `python3 -c` writing a file isn't caught. For unattended runs, the sandbox or container (§5.3) is the real boundary.
