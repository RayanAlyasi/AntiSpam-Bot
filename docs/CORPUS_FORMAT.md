# Corpus import format

`antispam corpus import` reads a labeled spam dataset and adds it to the confirmed-spam corpus
(DESIGN.md §7.8). This page describes the file. The README's "Importing a spam dataset" section
shows how to run the command.

```
antispam corpus import <file|-> --scope instance|group:<jid> [--holdout 0.2] [--holdout-clusters FILE] [--allow-ham] [--dry-run]
antispam corpus remove --import <id>
```

The file is either **JSONL** (one JSON object per line) or **CSV with a header row**. The importer
tells them apart by the first character: `{` means JSONL. Files may be UTF-8 with or without a
byte-order mark, with `\n` or `\r\n` line endings, up to 256 MiB. All examples here are
synthetic: never commit a real dataset.

## Fields

Only `text` is required. Unknown fields or columns (an ID, a date) are ignored.

| Field | Meaning |
|---|---|
| `text` | The message text, caption or other text surface. It may be empty only when `media_sha256` or `phash` is given. In JSONL it must be a string: a number would already have lost digits. At most 64 KiB. |
| `label` | `spam` (the default) or `ham`. Ham is never stored (see below). |
| `kind` | `text`, `link`, `image`, `video`, `sticker`, `document`, `audio`, `voice`, `contact`, `channel_share`, `invite`, `poll`, `location` or `event`. When missing, it's inferred: a group invite link → `invite`, a channel link → `channel_share`, media → `image`, another link → `link`, otherwise `text`. |
| `urls` | Extra links (links in `text` are found anyway). A JSONL array or string; in CSV, separated by spaces or `\|`. A link without a scheme gets `https://`. At most 50. |
| `media_sha256` | SHA-256 of the media or sticker file, 64 hex digits. A JSONL string or array; in CSV, separated by spaces or `\|`. |
| `phash` | 64-bit perceptual hash of an image, as hex (`0x` optional). |
| `language` | A language code such as `ar` or `en`. When missing, it's guessed from the script (only used in the report). |
| `category` | Free-form, such as `crypto`, `jobs`, `promo`. Lower-cased. Used to stratify the hold-out and in the report. |
| `source` | Free-form provenance note, such as `spam-only group export, 2026-10`. |
| `cluster_id` | Campaign or cluster identifier (a JSONL string or number). Records with the same id stay together when the hold-out is split. When missing, the importer groups related records itself. |

A record that fails validation is skipped and reported with its line number and the reason;
the rest of the file is still imported. Blank lines are skipped. Line numbers are the file's own
lines (in CSV the header is line 1, and a quoted cell can span lines).

## JSONL example

```json
{"text":"Earn 500 USD a day from home, no experience needed! Write to us on WhatsApp","category":"jobs","cluster_id":"jobs-01","source":"synthetic example"}
{"text":"Earn 500 USD daily from home, no experience needed!! Write to us on WhatsApp","category":"jobs","cluster_id":"jobs-01"}
{"text":"Join our VIP crypto signals group","urls":["https://chat.whatsapp.com/ExampleInviteCode1"],"kind":"invite","category":"crypto","language":"en"}
{"text":"","media_sha256":"3f79bb7b435b05321651daefd374cdc681dc06faa65e374e38337b88ca046dea","kind":"sticker","category":"adult"}
{"text":"Is the meeting still at 5 today?","label":"ham"}
```

## CSV example

```csv
text,label,category,cluster_id,urls
"Cheap followers for your page, 1000 for 5 USD",spam,promo,promo-07,https://followers.example/buy
"Medical leave notes in one hour, any clinic",spam,medical,med-02,
"Happy birthday, see you tonight!",ham,,,
```

## What the import does

1. **Validates** every record. `--dry-run` runs every step below in memory and writes nothing.
2. **Normalizes and fingerprints** each spam record with the bot's own code, exactly as when an
   admin confirms a message as spam: the text is redacted (third-party numbers, emails and
   mentions masked), the spam's own phone numbers are kept only as keyed hashes (`HASH_KEY`),
   and links, domains, invite codes and channel IDs are extracted. SimHash and MinHash
   fingerprints come from the redacted text.
3. **Dedupes**: exact copies (after normalization) and near-identical ones are merged into one
   entry, within the file and against the corpus already in the same scope, and counted.
   Re-importing a file stores nothing new.
4. **Holds out** `--holdout` (default 0.2) of the clusters for evaluation, so no campaign ends up
   on both sides. The share is spread over the category/kind groups, none giving more than its
   own share rounded up. Records with the same `cluster_id` form one cluster; a record without
   one joins every record it's related to (near-duplicates, loose ones included).
   `--holdout-clusters FILE` (one `cluster_id` per line) holds out exactly those clusters instead
   of a random split. Held-out records are never corpus entries; their redacted, normalized text
   and media hashes are kept in `eval_corpus`, and a record matching an earlier hold-out row is
   never stored as an entry.
5. **Measures recall**: the hold-out runs through the detection pipeline against the corpus as it
   will be after the import, in memory. Nothing is sent or recorded, and the AI tier isn't
   called, so an uncertain message counts as sent to admins. The report gives how many would have
   been deleted, sent to admins or missed.
6. **Stores** the rest as active corpus entries with `source = import` and the batch ID, in the
   chosen scope: every group (`instance`) or one group (`group:<jid>`). A running bot picks them
   up within 30 seconds.
7. **Reports** records read, rejected lines, spam and ham, entries imported, duplicates merged,
   clusters, the hold-out and its recall, the ham check, and every category or language with
   fewer than 10 examples.

## Ham records

`ham` records are never stored as text anywhere. They run through the pipeline in memory, and
the report says how many would have been deleted or sent to admins: a rough false-positive
check. With `--allow-ham` (group scope only, since allow-lists are per group) their
near-duplicate fingerprints, never their text, join the group's allow-list, so the bot never
treats a near-copy of them as known spam.

## Undoing an import

`antispam corpus remove --import <id>` deletes the batch's corpus entries, its hold-out rows,
its allow-list fingerprints and any rule learned only from its entries. The batch record stays,
marked removed, and running it again changes nothing.
