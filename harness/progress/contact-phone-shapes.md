# Phone shapes in contact.advertised (follow-up to holdout-recall)

Date: 2026-10-18. Branch `feat/contact-phone-shapes`, PR into `staging`. Fixes the judge's open
minors on PR #65. Contract: `.agent/contracts/contact-phone-shapes.md`.

## Done

- `internal/detect/contact`: the digit-run regexp takes the whole run (no 15-digit cap), and
  `phoneIn` tries the whole run, then splits it at its spaces into phone numbers, dates, years and
  short counts. Every part must be explained, so two numbers one space apart, or a number beside a
  date, count, while a 0-led group inside a spaced IBAN, card or account number doesn't (judge
  round 1 found that a plain search inside the run matched those).
- Not a phone: three or more 1–2-digit groups (times, prices, short dates, hyphen slots); a lone
  `0` first group (a price, not a trunk prefix).
- An Arabic letter right before a number with + or a trunk 0 is allowed ("واتساب0551234567"); a
  Latin letter before, or any letter or digit after, still marks a code.
- New tests: `internal/detect/contact/phone_shapes_test.go`; docs/DETECTION-RULES.md describes
  what counts as a national number.

## Follow-ups

- "0800 0930 1100" (24-hour times as 4-digit groups) reads as a phone, as on PR #65.
- A number written in pairs ("05 51 23 45 67", "+33 6 12 34 56 78") is now a miss (accepted,
  DET-002).
- An Arabic word glued to a country code without + ("رقم966551234567") is still a miss.
- The operator's corpus-import dry run (hold-out recall and the ham check) needs re-running on
  this branch; the dataset is private.
- Judge round 2 minors: "SA 44 2000 0001 2345 6789 2012" (IBAN spaced after the country code,
  year-like last group) and "حساب 0108 0952 3456 0789 1234 56" (two 0-led parts) still read as
  phones. Possible fix: a 4-digit year only beside 1–2-digit date groups, or reject a split whose
  parts are all 4-digit groups.
