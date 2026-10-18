# Hold-out recall of the corpus import: findings (post-v0.1.0)

Date: 2026-10-18. Branch `feat/holdout-recall`. Aggregates only: the dataset is private and
stays outside the repo, and no message from it is quoted here or in tests.

## Baseline

`antispam corpus import <corpus.jsonl> --scope instance --holdout-clusters <file> --dry-run`
against an empty Postgres: 224 corpus entries, 68 held out in 55 clusters, **0 deleted, 22 sent
to admins, 46 missed (32%)**. Staging reported 23 / 44 of 67 (34%); its live corpus dedupes one
record differently. No AI tier in either number.

## Why the 46 were missed

- **Nothing fired at all.** All 46 have zero signals. With an empty rule table, the only analyzer
  that can see an unseen campaign is the corpus near-duplicate matcher, and its loose band
  (MinHash Jaccard ≥ 0.4 or SimHash ≤ 12 bits) is the whole of today's recall: all 22 caught
  messages are `corpus.similar`, one with an extra `text.invisible_chars`.
- **They are close to the corpus, just not by word-set Jaccard.** The best corpus match is in the
  same category for 42 of 46, at Jaccard 0.12–0.39 (SimHash 18–34 bits). The messages are
  reworded "list of services + contact" ads drawn from a small, shared vocabulary.
- **Short vs long.** A short ad whose words almost all appear in a long ad scores a low Jaccard,
  because the long ad's other words count against it. Containment (shared terms over the smaller
  side's terms) captures it; Jaccard can't.
- **Noise terms.** URL pieces (`https`, `chat`, `whatsapp`, `com`), the `[phone]` redaction
  placeholder, function words and greetings count as words. On ordinary chat they make up most
  of what overlaps with the corpus (a shared group link alone gives 4 of a message's terms).
- **Arabic clitics.** `و` / `ال` / `بال` / `لل` prefixes split one word into several tokens,
  so the same service list written with and without them shares fewer terms.
- **A contact channel is almost always there.** 61 of 68 held-out messages carry a phone number,
  `wa.me` / `api.whatsapp.com/send` link, group invite, `t.me` link or channel link; 4 of 158
  ordinary messages do.
- **By kind:** text 19, link 11, contact (vCard) 8, invite 7, channel share 1. By language:
  Arabic 42, English 4 (investment-group invites, which share structure but little wording).

## What was measured

- Development set: leave-one-campaign-out over the 224 corpus entries (each matched against the
  entries of other clusters), so thresholds were picked without looking at the hold-out.
- False positives: 158 real ordinary-chat messages from the same source (kept outside the repo)
  and 159 synthetic hard negatives written for this check (student-group chatter that names
  assignments, research, sick leave, medical reports, group links and phone numbers).
- Containment alone, on cleaned terms, at ≥ 0.5 reaches 49/68 with no false positive on either
  set, but it can't tell "I got a sick note and a medical report" from an ad for them: those
  ordinary messages reach containment 0.67. What separates them is the contact channel.

## What was built

1. **Match terms** (`neardup.Terms`, `internal/detect/neardup/terms.go`): words of the normalized
   text without links, the words that only describe a link (join, group, `رابط`, `قروب`, …),
   redaction placeholders (a kept number's id stays), numbers under 3 digits, and a closed list
   of Arabic and English function words and greetings. An Arabic word loses one article
   (وال بال فال كال لل ال) and then a lone و, each only when 3 letters remain. SimHash and
   MinHash are unchanged, so the CERTAIN band and stored allow-list fingerprints don't move.
2. **`corpus.related`** (+2.5, below T_review alone): a third closeness level, `Related`, on
   term Jaccard ≥ 0.2, or containment ≥ 0.4 with at least 6 terms on the smaller side.
   `FingerprintNorm` carries the term hashes; the live corpus reload and the import replay use
   it. The import planner's dedupe and clustering keep the plain fingerprint.
3. **`contact.advertised`** (+1.0, T0, `internal/detect/contact`): a phone number (E.164, or a
   phone-shaped run of 9–15 digits outside links and mentions: leading + or 0, or 11+ digits
   written together; never a date, time, schedule, year list or ID), a contact card, a `wa.me` /
   send link, a group or channel invite or link, a Telegram link.
4. Weights version `2026-10-18.1`. Docs: DESIGN §7.5 item 2, docs/DETECTION-RULES.md.

## Results (dry run against an empty Postgres, no AI)

| | before | after |
|---|---|---|
| Operator's hold-out (68) | 22 to admins, 46 missed (32%) | 54 to admins, 14 missed (79%) |
| Default random 20% split (39) | 7 to admins, 32 missed (18%) | 25 to admins, 14 missed (64%) |
| 158 real ordinary messages + 159 synthetic hard negatives | 0 flagged | 0 flagged |

Before the judge's review, a national-number check that trusted the redactor gave 55/68; it also
fired on dates, times and year lists, so ordinary chat with a date could reach admins. The
phone-shape check costs one hold-out message. Most catches are related + contact or similar +
contact; the misses either offer a contact with too little shared wording, or share wording
without a contact.

During the build a short message sharing a group link and two or three generic words ("link",
"group", "new") reached related + contact on two hard negatives. Treating the words that only
describe a link as noise fixed both without a shared-term floor (a floor of 4 also worked, cost
2 hold-out catches, and contradicted the contract's Jaccard rule on short texts).

## Follow-ups

- F-151: recall per band and type. The 81% is "sent to admins": the AI tier isn't run in the
  replay, so how much of it the AI would delete is unmeasured.
- The related Jaccard path has no minimum term count (the contract's rule): a 3-term corpus entry
  relates to short ordinary messages. With the contact check tightened this needs a contact too,
  but it is worth watching in the F-151 per-band report.
- A contiguous 11–15 digit run (an order number) counts as a phone number written with its
  country code; `contact.advertised` is +1.0 only.
- The 10 remaining contact-only misses are rewordings with little shared vocabulary; few-shot
  AI (F-140) is the intended net for them.
- `corpus.related` and `contact.advertised` weights are hand-set; the weekly re-weighting
  (F-215) should refit them once there are review decisions.

## Not proposed

- Lowering the loose Jaccard limit: 0.3 gains 9 hold-out messages but has no principled stop, and
  on raw tokens the URL pieces drive it.
- Containment alone as a loose match: the sick-note case above.
- Category keyword lists ("sick leave", "assignments"): they would fit this dataset's two big
  categories and nothing else.
