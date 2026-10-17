# WP-G bundle: detection rules, lists and the number blacklist

Twelve features in one build: F-039, F-041, F-042, F-043, F-044, F-045, F-048, F-049, F-102, F-113,
F-123, F-127. Hidden acceptance tests exist only for the core features **F-123** and **F-127**
(`acceptance/blacklist/`; the store and end-to-end tests carry the `integration` build tag). The
other ten are proven by the builder's own table, property and fuzz tests in their packages; no
hidden tests cover them. Sibling dependencies (`internal/envelope` F-024, `internal/detect` F-040,
`internal/normalize` wp-a, `internal/store/storetest` wp-c) are still landing, so the acceptance
tests compile only once those merge.

## F-123 Blacklist table and number normalization (core)
**Goal:** `number_blacklist` stores E.164 numbers with keyed hashes, scope and reason, and the
`.blacklist` arguments in every common format normalize to E.164 with invalid numbers reported.
**Behaviour:**
- `ParseArgs` accepts `+966 55 123 4567`, `00966551234567`, `0551234567` (region `SA`), Arabic-Indic
  digits, `wa.me/966551234567` with or without `https://`, and foreign numbers in international
  form whatever the region. Several numbers per command, in written order, deduplicated by E.164.
  The reason starts at the first piece that isn't part of a number and is trimmed. Digits inside
  the reason stay in the reason. Pieces that look like a number but aren't valid go to `Invalid`
  in their written form (consecutive bad pieces joined by one space); they are never dropped.
  With an empty region, national forms are invalid and only international forms parse.
- `Hasher.Hash` is the lowercase hex HMAC-SHA256 of the E.164 string under the key (equal to
  `redact.PhoneHash`). Keys under 16 bytes fail with `ErrShortKey`. `Mask` shows `+`, the first 3
  digits and the last 2, with `•` for each hidden digit.
- Store: adding an active number again in the same scope is idempotent (existing entry, `false`).
  The same number may be listed in another group and instance-wide. Remove returns `false` when
  nothing is active, and re-adding after removal creates a new entry. List is the group's own
  entries, newest first. Check returns the group's own and the instance-wide active entries.
  Invalid E.164, a non-hex hash, an unknown scope, a group entry without a group JID or an instance
  entry with one are errors. `RulesLoader.LoadBlacklist` returns only active entries.
**Public surface:** `internal/detect/blacklist`: `ParseArgs`, `Parsed{Numbers, Invalid, Reason}`,
`NewHasher`, `Hasher.Hash`, `ErrShortKey`, `MinKeyLen`, `Mask`. `internal/store`: `AddBlacklist`,
`BlacklistAdd`, `RemoveBlacklist`, `ListGroupBlacklist`, `CheckBlacklist`, `BlacklistEntry`,
`RulesLoader{Q db.DBTX}` (implements `rules.Loader`). Test DB: `storetest.Pool(t)`.

## F-127 Blacklisted number in content is known spam (core)
**Goal:** a blacklisted number in a vCard `waid`/`TEL`, a `wa.me` link or advertised in text is
CERTAIN `known` evidence, and removing it from the list lifts the signal.
**Behaviour:** after `textnorm.Prepare(e, region)`, the T0 analyzer `number.blacklist` emits exactly
one signal `{Code: "number.blacklisted", Known: true, Hard: false, Weight > 0}` (spam, not a policy
violation) when any number in the message is on this group's list, or on the instance list when
the group uses it. One signal per message, however often or wherever the number appears. Sources:
vCard waid, vCard TEL (international or national with the region), `wa.me` and
`api.whatsapp.com/send?phone=` links in `e.URLs` or only in the text, and numbers written in any
text surface (international, national, Arabic-Indic digits, spaced digits, zero-width characters
inside). Evidence names the masked number and where it was found and never contains the full
number in any form. No signal for unlisted or near-miss numbers, another group's entries, instance
entries when `UseInstanceBlacklist` is false (or `Settings` is nil), or after the entry is removed
and the holder reloads. The group comes from the envelope even if `Settings` says otherwise.
`Applies` is true for messages that carry a number. Analyze is safe during `Reload`.
**Public surface:** `rules.BlacklistEntry`, `rules.BlacklistGroup/Instance`, `rules.Loader`,
`rules.NewHolder`, `Holder.Reload/Current`, `rules.ViewOptions`; `ruleview.Source{Rules, Settings}`;
`textnorm.Prepare`; `blacklist.New[P]`, `Analyzer.Name/Tier/Applies/Analyze`, `CodeBlacklisted`.

## Non-core features (builder tests only)
- **F-039** (`textnorm`): every text surface gets `Norm`, `Skeleton`, `Leet`, `Loose`, `Stripped`,
  `EmojiTokens` per §7.2 with `Raw` untouched; `e.Phones []PhoneRef{E164, Source}` from text and vCards.
- **F-041** (`rules`, `store`): rules and fingerprints validated, compiled into an immutable
  `Snapshot`, swapped atomically by `Holder`, refreshed by LISTEN/NOTIFY within a second plus a resync.
- **F-042/F-043** (`textmatch`, `textrules`): Aho-Corasick keyword/phrase and RE2 regex over the
  normalized variants, matched phrase as evidence; regexes over length/complexity limits rejected at save.
- **F-044** (`textmatch`, `textrules`): token-set similarity and Damerau-Levenshtein near-misses of
  short phrases; weighted `rules` signals only, never `Known`.
- **F-045** (`script`): mixed scripts inside a word and invisible-character counts are weighted
  signals; ordinary Arabic/English bilingual messages don't fire.
- **F-048** (`domain`): group allow beats global deny, group deny beats global allow; a deny hit is
  `Known`; an allow-listed domain never fires.
- **F-049** (`exact`): blocklisted media/sticker SHA256, channel JIDs, invite codes and advertised
  numbers (keyed hashes) are `Known`, without downloading anything.
- **F-102** (`fingerprint`): 64-bit SimHash and MinHash from normalized text, LSH indexes for
  Hamming/Jaccard candidates.
- **F-113** (`corpus`, `store`): corpus entries, confirmations and learned rules; label spam only.

**Out of scope:** pattern keys and near-duplicate matching against the corpus (F-117), the
`.blacklist` command handler and PN→LID resolution, kicks and join handling (§9.8 steps 1–3),
review routing for instance entries in ask-admins groups, the website.

**DESIGN.md refs:** §5.3, §6, §6.1, §6.2, §6.4, §6.8, §6.9, §7.1, §7.2, §7.3, §7.5, §9.8, §12.1,
§12.3, §14, §17.

## Assumptions (conservative readings)
- Hard text rules (keyword/regex) are policy violations (`Hard`), not `Known`. Hard exact rules
  (domain, URL prefix, number, channel, invite, sha256) and spam fingerprints are `Known`. Fuzzy
  matches are never `Known`.
- Group allow-list beats global deny; group deny beats global allow.
- A blacklist hit is spam (`Known`), not a policy violation, so `Hard` is false.
- With an empty region, `00`-prefixed input is not tested either way; `+` and `wa.me` forms must work.
- `.blacklist list` shows only the group's own entries; the tests don't add instance entries there.

## Acceptance tests (`acceptance/blacklist/`)
- `TestParseArgsFormats`: each accepted written form normalizes to the same E.164.
- `TestParseArgsSeveralNumbersAndReason`: order kept, duplicates removed, reason split and trimmed.
- `TestParseArgsInvalidReported`: invalid pieces reported in written form, never dropped.
- `TestParseArgsEmptyRegion`: national forms invalid without a region; international ones parse.
- `TestHasherKeyedHash`: HMAC-SHA256 hex, keyed, deterministic, short keys rejected.
- `TestMask`: masked form keeps only `+`, the first 3 and last 2 digits.
- `TestBlacklistAnalyzerIdentity`: name `number.blacklist`, tier T0.
- `TestBlacklistedNumberInContentIsKnown`: every source and obfuscation gives one `known` signal.
- `TestBlacklistOneSignalPerMessage`: repeated and group+instance hits give one signal.
- `TestBlacklistEvidenceMasksNumber`: evidence has the mask and never the full number.
- `TestBlacklistNoSignal`: unlisted, near-miss, other-group and opted-out instance entries stay silent.
- `TestBlacklistGroupComesFromEnvelope`: `Settings` can't redirect the group.
- `TestBlacklistRemoveLiftsSignal`: add + reload fires; remove + reload stops it.
- `TestBlacklistAnalyzeDuringReload`: concurrent Analyze and Reload are race-free.
- `TestStoreAddBlacklistIdempotent`, `TestStoreAddBlacklistScopes`, `TestStoreAddBlacklistRejectsInvalid`,
  `TestStoreRemoveAndReAdd`, `TestStoreListGroupNewestFirst`, `TestStoreCheckBlacklist`,
  `TestStoreRulesLoaderActiveOnly`, `TestStoreBlacklistEndToEnd` (integration): the table semantics
  above, and store → loader → holder → analyzer, with removal lifting the signal.
