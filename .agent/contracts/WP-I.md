# Contract WP-I: policy and enforcement

Bundle: **F-052, F-053, F-054, F-055, F-056, F-057, F-058, F-059, F-060, F-061, F-062, F-063, F-076, F-077**.
Hidden acceptance tests (`acceptance/policy`, pure Go, `rapid`) cover only the core decision invariants
in `internal/policy`. Everything else is builder-tested (list below).

**Goal:** turn a scored finding, the sender's standing and the group's settings into one §8.3 route and
an action plan, so that no code path bans someone the bot isn't allowed to ban.

## Behaviour (tested)
- **Spam** = `BandCertain`+`SourceKnown`, or `BandUncertain`+`SourceAI`. Any other non-clean finding
  without violations is *still uncertain*: `Review{Kind: uncertain}`, no ban, no delete unless
  `Uncertain.DeleteWhilePending` (and never in ask_before_action). `SourceRules` never deletes or bans.
- **Spam from a new/unknown member, mode auto:** cell = `matrix[f.Type].Effective(f.Source)`.
  `allow` → `RouteAllow`, nothing. `delete_only` → delete, no ban, no review. `ask` → `RouteAsk`, delete,
  `Review{ban_decision, ProposeBan}`. `auto_ban` → delete; with `SourceKnown` also `Ban`, `RouteAutoBan`.
  A ban happens only when the effective cell is `auto_ban`. Verdict is `spam` for all of these.
- **`Cell.Effective(src)`:** non-auto_ban cells return their action for every source. `auto_ban` returns
  `auto_ban` only if src is in `RequireSource` (empty = `[known]`; `SourceNone` never matches), else
  `Else` (empty or `auto_ban` = `ask`).
- **Established or Compromised** members: never `Ban`, never `RouteAutoBan`, review `OnTimeout` never
  `auto_ban`. Their spam is still deleted (unless cell `allow` or ask_before_action); when the cell is
  `auto_ban`/`ask` admins get a review with `ProposeBan`, `Kind: hacked` if Compromised.
- **Admins (admin/superadmin), the bot, allow-listed:** never `Delete`/`Ban`; review only `Kind: alert`
  with `ProposeBan=false`; the plan holds no delete or ban.
- **Ask modes** (`ask_delete_first`, `ask_before_action`), for any settings (even ones `Validate`
  rejects), finding and standing: never `Ban`, never `RouteAutoBan`, no review with `OnTimeout:
  auto_ban` (sanitize it), no ban action in the plan. ask_delete_first: spam (cell auto_ban/ask) →
  delete + `ban_decision` review. ask_before_action: **never `Delete`** (nor a delete action);
  spam → `Review{Kind: ask_before}`.
- **Policy-only** (violations, no spam): under every preset's defaults → `VerdictPolicy`,
  `RoutePolicy`, `Delete`, no ban, no review, plan = exactly one delete at P1, however many
  `RecentViolations`. Spam + violation follows the spam route (even if milder than `policy.action`).
- **Identity priors:** a finding with ≥1 positive prior signal, no positive non-prior signal, no
  violations, and `Source` none/rules → no delete, ban or review; empty plan.
- **Decide ignores `Settings.Mode`**; `PlanFor` applies it. Paused → `Plan{}` (no record, no actions).
  Shadow → `Enqueue` empty; non-clean → `Record`, `Mode: shadow`, `Would` = the decided actions.
  Enforce → non-clean `Record`, `Mode: enforce`, `Enqueue == Would`. Clean → nothing recorded.
  One action per decided step: delete P1, ban P2, review P3 (carrying the `ReviewRequest`).
- **Presets** fill the §9.2 grid exactly (compared by meaning); Ask admins sets `ask_delete_first`, the
  others `auto`. `ApplyPreset` resets the grid; matrices are never shared between documents; unknown
  presets error. `Defaults()` = Standard, enforce, silent, auto, 7 d/20 msgs, rejoin block,
  delete_while_pending false, policy delete_only, repeat nil, review 6h/keep_deleted_no_ban.
  `Validate` rejects `on_timeout: auto_ban` in ask modes.

## Public surface (`internal/policy`, stub in `policy_stub.go`)
Exactly the surface in the delegation message: `Preset*`, `Mode*`, `Enforce*`, `ContentType`/`Type*`,
`ContentTypes`, `CellAction`/`Cell*`, `Source*`, `Band*`, `Violation*`, `Role*`, `Cell.Effective`,
`Settings{Preset, Mode, Silent, Enforcement{Mode, Established, Ban, AutobanMatrix}, Uncertain, Policy
{Action, Repeat}, Review{Timeout, OnTimeout}}`, `Duration`, `Timeout*`, `Defaults`, `PresetSettings`,
`ApplyPreset`, `Validate`, `Signal`, `Violation`, `Finding`, `Standing`, `Route*`, `Verdict*`,
`Review*` kinds, `ReviewRequest`, `Decision`, `Decide`, `Action*`, `DetectionMode`/`Detection*`,
`Plan`, `PlanFor`. Additions used by tests: sub-struct type names `Enforcement`, `Established`, `Ban`,
`Uncertain`, `ViolationPolicy`, `Repeat`, `Review` (the builder may add fields freely).

## Builder-tested (no hidden tests)
F-052 just-joined weight/decay/window; F-053 detections store (signals, route, rule version,
action_plan, mode); F-054 settings JSON/schema, dotted keys get/set, history, audit, NOTIFY; F-055 preset
extras (Strict T_delete 0.80) and diff-from-preset; F-056 content policy allow/block/block_for_new/
block_for_untrusted incl. extensions; F-057 trust/established computation; F-061 policy.action ask/ban
and repeat; F-063 threshold monotonicity, ladder inert; F-076 ban cap → one review item, deletion
circuit, kill switches; F-077 local ban (group_bans, kick at P2, rejoin kick at P0, unban).

**Out of scope:** network bans, raid mode, review delivery/votes, ladder behaviour, AI track record.
**DESIGN.md refs:** §7.0, §7.3, §8.1–§8.4, §8.6, §8.7, §9.1–§9.3, §9.5 (timeouts), §9.6, §10.2, §10.4,
§12.1, §12.2, §14, §18.

## Assumptions (conservative readings)
1. ask_before_action deletes nothing at all, policy violations included ("nothing is deleted or banned
   until an admin decides", §8.3 (b)).
2. A hard violation without known spam arrives as `BandCertain`+`SourceNone` (scoring contract F-040);
   it is policy-only, not "still uncertain".
3. `Else: auto_ban` is invalid and read as `ask`. An `ai` source in `RequireSource` is not asserted
   either way (the track-record gate lives in settings, §9.2).
4. Admin exemption wins over `compromised.admins: delete_known_spam` (opt-in; not in the tested surface).
5. Policy-only messages from established members are still deleted (rules apply to everyone).
6. go.mod/go.sum (rapid) are outside the stamp-exempt paths, so they are left modified in the working
   tree for the builder's stamped commit.

## Acceptance tests (`acceptance/policy`)
- `TestAskModesNeverAutoBan`: property; ask modes never ban/route auto_ban/timeout-ban; (b) never deletes.
- `TestAskDeleteFirstDeletesSpamAndAsksForBan`: (a) deletes known and AI spam, ban goes to admins.
- `TestAskBeforeActionLeavesMessageAndAsks`: (b) leaves spam visible, ask_before review, no-ban fallback.
- `TestPolicyViolationAloneNeverBansByDefault`: property; every preset: silent delete at P1, no ban/review.
- `TestSpamAndViolationFollowsSpamRoute`: spam route wins over policy.action in both directions.
- `TestEstablishedAndCompromisedNeverAutoBanned`: property; never banned, spam still deleted, admins asked.
- `TestExemptSendersNeverActedOn`: property; admins, bot, allow-listed: no delete/ban, alert only.
- `TestIdentityPriorsNeverActAlone`: property; priors-only findings produce no action or plan.
- `TestStillUncertainGoesToAdminsNothingDeletedByDefault`: property; uncertain review, no ban, visible.
- `TestMatrixCellSemanticsInDecide`: property; allow/delete_only/ask/auto_ban+require_source in Decide.
- `TestDefaultRouting`: the §8.3 table rows under Defaults().
- `TestCellEffective`: cell semantics incl. default require_source and else.
- `TestContentTypesInGridOrder`, `TestPresetMatrixMatchesDesignGrid`, `TestDefaultsAreStandardAndConservative`,
  `TestApplyPresetResetsMatrix`, `TestPresetMatricesAreIndependent`, `TestUnknownPresetRejected`,
  `TestValidateRejectsAutoBanTimeoutInAskModes`: presets and defaults per §9.2/§12.1/§12.2/§9.5.
- `TestShadowModeEnqueuesNothingButRecords`, `TestPausedModeDoesNothing`, `TestEnforceModeEnqueuesDecision`,
  `TestShadowModeRecordsWouldBeBan`: PlanFor per run mode, priorities per §10.2.
