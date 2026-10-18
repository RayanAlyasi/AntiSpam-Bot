# Test-change request: committed and acceptance tests pin the old ask-before fallback (delete_no_ban)

**Status:** open. Feature `ask-before-keep`, branch `feat/ask-before-keep-fallback`.

## Tests

`make check-fast` (`.agent/logs/check-test.log`):

```
--- FAIL: TestDocSettings (0.05s)
    settings_test.go:58: mode (b) review config = 15m0s leave, want 15m delete_no_ban from enforcement.ask_before
--- FAIL: TestParseStoredFillsNewKeys (0.02s)
    stored_test.go:25: ParseStored = limits {MaxBansPerHour:4 MaxDeletionsPerMinute:30}, ask_before {Timeout:900000000000 OnTimeout:leave}; want defaults for the new keys and the stored cap kept
--- FAIL: TestRoutes/known_spam,_ask_(b) (0.00s)
    decide_test.go:302: got review ask_before on timeout leave, want ask_before on timeout delete_no_ban
--- FAIL: TestRoutes/violation,_ask_(b) (0.00s)
    decide_test.go:302: got review ask_before on timeout leave, want ask_before on timeout delete_no_ban
```

| File | Line | What it pins |
|---|---|---|
| `internal/api/chat/settings_test.go` | 57–58 | `ReviewConfig` of a group switched to ask_before_action (defaults otherwise) is `TimeoutDeleteNoBan` |
| `internal/policy/stored_test.go` | 24 | `ParseStored` fills a missing `enforcement.ask_before` with `TimeoutDeleteNoBan` |
| `internal/policy/decide_test.go` | 259, 275 | `TestRoutes` rows "known spam, ask (b)" and "violation, ask (b)" expect `TimeoutDeleteNoBan` with default settings |

Hidden acceptance tests (F-122), `f122_timeouts_test.go` in the wpk acceptance package:

```
--- FAIL: TestF122_EffectiveTimeout/ask_before_action_defaults (0.00s)
    f122_timeouts_test.go:43: got (15m0s, leave), want (15m0s, delete_no_ban) (a timeout must never become a ban the bot isn't allowed to make, DESIGN §9.5)
--- FAIL: TestF122_EffectiveTimeout/auto_ban_in_ask_before_action (0.00s)
    f122_timeouts_test.go:43: got (15m0s, leave), want (15m0s, delete_no_ban) (a timeout must never become a ban the bot isn't allowed to make, DESIGN §9.5)
--- FAIL: TestF122_ReviewCommands/ask_before_action_delete (0.00s)
    f122_timeouts_test.go:223: review.on_timeout = "keep_deleted_no_ban", want "delete_no_ban" set by 100000000000001@lid (DESIGN §13.7)
    f122_timeouts_test.go:227: review.timeout = "6h", want a duration of 2h (DESIGN §13.7)
--- FAIL: TestF122_ExpireAppliesFallbackOnce/ask_before_action_deletes,_no_ban (0.00s)
    f122_timeouts_test.go:136: got 0 deletes of the message, want delete=true (DESIGN §9.5 fallback leave)
```

- `TestF122_EffectiveTimeout` (two cases) and `TestF122_ExpireAppliesFallbackOnce` (one case)
  expect the old mode (b) default, delete_no_ban, for an unset fallback and for a downgraded
  auto_ban. Nothing still bans, so the invariant they cite holds. Only the default changed.
- `TestF122_ReviewCommands/ask_before_action_delete` expects `.review timeout 2h delete` in an
  ask_before_action group to write `review.timeout` and `review.on_timeout`. That mode ignores
  those keys (`review.ConfigFrom` takes `enforcement.ask_before.*`), so the command used to do
  nothing there, although the maintainer was told to use it. It now writes
  `enforcement.ask_before.timeout = 2h` and `enforcement.ask_before.on_timeout = delete_no_ban`,
  as the contract `.agent/contracts/ask-before-keep.md` and `TestAskBeforeKeep_ReviewTimeoutCommand` require.

## Why they conflict

Maintainer decision for v0.1.0 (2026-10-18, relayed by the coordinator session): the bot must
never delete or ban unless an admin says so. In `ask_before_action` the default timeout fallback
is "keep": the message stays up, with no strike, no ban and no trust restore. DESIGN.md §8.3 (b)
and §9.5 Timeouts in this change now say `enforcement.ask_before.on_timeout` defaults to `leave`.
The three unit tests use the defaults and assert the old `delete_no_ban` default. None of them sets
`delete_no_ban` explicitly. Tests that set it explicitly (`asking_test.go`) still pass.

## What I believe is correct (confidence: high)

Change only the expected default, keeping what each test checks:

1. `settings_test.go:57–58`: want `review.TimeoutLeave` ("15m leave from enforcement.ask_before").
2. `stored_test.go:24`: want `TimeoutLeave` for the missing key.
3. `decide_test.go:259` and `:275`: want `TimeoutLeave` in the last column.
4. `f122_timeouts_test.go`: the two ask_before_action rows of `TestF122_EffectiveTimeout` want
   `leave`. The "ask_before_action deletes, no ban" row of `TestF122_ExpireAppliesFallbackOnce`
   sets `OnTimeout: delete_no_ban` explicitly, so it keeps testing a delete (or it expects no
   delete with the default). The ask_before_action row of `TestF122_ReviewCommands` checks
   `enforcement.ask_before.timeout`/`.on_timeout` instead of `review.*`.

Alternative for `TestF122_ReviewCommands` only: the command writes both pairs, so the old test
passes too. I didn't do that, because it stores a `review.on_timeout` that does nothing in this
mode, and that confuses things again if the group later leaves the mode.

`delete_no_ban` stays covered where a group chooses it: `internal/policy/askbefore_test.go`,
`internal/review/askbefore_test.go`, `internal/api/chat/askbefore_timeout_test.go`, and the
acceptance tests in `.agent/contracts/ask-before-keep.md`.

## What I tried

Nothing that touches these tests. The implementation follows the contract. Every other package
passes, including the new unit tests and the migration integration test. Keeping
`delete_no_ban` as the default would contradict the maintainer's decision.
