# Detection rules, lists and the number blacklist

How the bot matches messages against its block and allow lists, the manual phone-number blacklist
and the confirmed-spam corpus. The design is in `docs/DESIGN.md` §6, §7.2–§7.5, §9.8, §12.3 and §14.

## Rules

Rules live in the `app.rules` table. Each rule has:

| Field | Values | Meaning |
|---|---|---|
| `scope` | `global`, `bot`, `group` | Every group, the groups of one bot number (`scope_id` = bot JID), or one group (`scope_id` = group JID). |
| `pack` | text | Global rules only. A rule in a pack applies only to groups that subscribe to it (`blocklist_refs`, e.g. `global:crypto`). Rules without a pack apply everywhere. |
| `type` | `keyword`, `phrase`, `regex`, `domain`, `url_prefix`, `phone_number`, `channel`, `invite`, `sha256`, `phash`, `simhash` | What the pattern is. |
| `action_class` | `hard`, `weighted`, `allow` | What a match means (below). |
| `weight` | −20 to 20 | Log-odds weight for weighted rules (and the explanation order of hard ones). |
| `enabled` | true / false | Disabled rules are ignored. |

**What a match means.**

- A **hard** domain, URL prefix, phone number, channel, invite or media hash is *known spam*: the
  message is CERTAIN and follows the auto-ban matrix.
- A **hard** keyword, phrase or regex is a *policy violation*: the message is deleted silently,
  without a ban. Keywords aren't on the list of evidence strong enough to ban (§7.3).
- A **weighted** rule adds its weight to the spam score. On its own it can only send a message to
  the AI or to the admins.
- An **allow** rule (domains, URL prefixes, numbers, channels, invites, hashes) exempts that value
  from deny rules in the same or a wider scope.

**Precedence.** Group rules are checked before bot rules, which are checked before global ones.

- An allow decides at once, so a group's allow-list overrides a global deny-list.
- A hard deny (and, for exact values, a spam fingerprint) also decides, so a group's hard
  deny-list overrides a global allow-list.
- A weighted rule never hides stronger evidence. Its weight counts, but the check goes on, and a
  hard rule, spam fingerprint or allow in a wider scope still decides.

Within one scope, the most specific domain or the longest URL prefix counts, and allow beats deny
for the same value. Domains in the group settings (`allowlist.domains`, `content.link_domains.allow`)
are allowed before any rule is checked. An allow-listed domain never fires.

For each link, the domain lists are checked first, then the URL prefixes, then confirmed-spam URL
fingerprints. A weighted domain doesn't stop that chain, so a known-spam link under a merely
suspicious domain is still caught.

**Patterns.** When a rule is saved, its pattern is normalized and stored as `norm_pattern`:

- Keywords and phrases go through the text normalizer and are compared as words, ignoring case,
  punctuation and spacing. `free-bitcoin!!` matches the phrase `free bitcoin`. A keyword matches only
  whole words, so `free` doesn't match `freedom`.
- Text is matched in every normalized form: plain, confusables skeleton, leetspeak fold and the
  Arabic match form. So `fr33`, `frее` (Cyrillic е) and `f.r.e.e` all match `free`.
- The confusables skeleton is only used when the text has a non-ASCII letter. On plain ASCII text
  it would only add false matches (`.com` skeletonizes to `corn`, `101` to `lol`).
- Domains are stored lower-case and in punycode. A domain rule covers its subdomains, and a leading
  `*.` is ignored.
- URL prefixes are stored without the scheme, so `http` and `https` links match alike. Paths are
  case-sensitive.
- Hashes are lower-case hex.
- Phone-number rules store the number's keyed hash.

**Regex limits.** Regexes use Go's RE2 syntax (linear time, no backreferences) and match
case-insensitively on the normalized text. A pattern is rejected when it's saved if it is:

- longer than 512 characters;
- compiles to more than 5,000 instructions (`((abc|def){30}){30}` is too complex);
- matches empty text, so it would match every message.

Regexes scan the first 4,096 bytes of each text surface; keyword, link and number checks always
see the whole text.

**Fuzzy matching.** Phrases of 6 or more characters also match near-misses:

- up to 1 typo for phrases of up to 10 characters, 2 up to 20, and 3 beyond;
- the same words in another order, for phrases of 3 or more words.

A fuzzy match is always a weighted signal, never CERTAIN, even for a hard rule. Fuzzy matching
looks at the first 200 words of each text surface, so a padded message can't make it slow.

**Script anomalies.** Two signals look for obfuscation:

- Words that mix look-alike scripts (Latin with Cyrillic, Greek, Armenian or Cherokee) add +1.5,
  plus 0.5 for each further word, up to +3.
- Hidden characters stripped from the text add +0.5 from 3 characters, +1.0 from 10 and +1.5
  from 30.

Arabic and English in the same message is normal and doesn't fire.

## Fingerprints

`app.fingerprints` holds known values that weren't typed in as rules: confirmed or imported media
hashes, channels, invites, keyed number hashes and URLs.

- A `spam` label is known-spam evidence.
- An `allow` label (left behind when admins overturn a detection) stops that value from ever matching
  again in its scope.

## Reloads

Each node keeps every enabled rule, fingerprint and active blacklist entry in memory. A trigger on
the three tables sends `NOTIFY rules_changed` on every change, and the rules holder reloads within a
second. (The holder and its listener are started with the detection pipeline, which wires them.)
Hit counters don't trigger reloads. The bot also reloads every 5 minutes, and after its LISTEN
connection reconnects, so a lost notification is never permanent. If a reload fails, the previous
rules stay in use. An invalid row is logged and skipped; it doesn't disable the others.

## Number blacklist (§9.8)

`app.number_blacklist` holds numbers that admins blacklisted for one group, or that the operator
blacklisted instance-wide. A group uses instance entries unless `blacklist.use_instance` is false.

**Accepted forms.** `.blacklist add` takes one or more numbers followed by an optional reason, in
any common form:

- `+966 55 123 4567`;
- `00966551234567`;
- `0551234567`, using the group's default country;
- Arabic-Indic or Persian digits;
- `wa.me/966551234567`.

Every number is normalized to E.164. Pieces that look like numbers but aren't valid are reported
back, not dropped. Adding a number that's already listed changes nothing. Removing it keeps the row,
with who removed it and when, for the audit.

**Content matching.** A blacklisted number in a contact card (waid or TEL), in a `wa.me` link or
written in the message text is CERTAIN known spam. Matching uses the number's **keyed hash**
(HMAC-SHA256 with the deployment's key), so the in-memory lists and the corpus never hold the number
itself. Removing the entry lifts the signal at the next reload.

**Display.** Evidence shows numbers masked: `+966•••••••67`.

## Confirmed-spam corpus (§7.5)

`app.corpus_entries` holds one entry per confirmed spam message. Its label can only be `spam`.

**What an entry keeps:**

- the **redacted** text and its normalized form. Third-party numbers, emails, mentions and names
  are masked, and the spam's own numbers become keyed-hash tokens;
- a SimHash and a MinHash of the text;
- the links (redacted like the text; `wa.me` links aren't kept, only their numbers' hashes), the
  eTLD+1 domains, channel JIDs, invite codes and media SHA256s;
- the keyed hashes of the numbers it advertises;
- where it came from.

WhatsApp's own domain is never stored as a learned domain; invite and channel links keep their
target instead.

**Promotion.** `app.corpus_confirmations` records one confirmation per pattern per group. The rule
that promotes a pattern after 3 unrelated groups confirm it counts these.

**Overturns.** `app.learned_rules` links rules to the entry they were learned from. When admins
overturn an entry, its text is erased and the rules learned only from it are deleted. A rule that
another live entry also taught stays.
