# contact-phone-shapes: tighter national-number shapes for contact.advertised (follow-up to holdout-recall)

**Goal:** `contact.advertised` counts a national phone number only when it really is shaped like one. Times, prices and short dates stop matching (false positives, the priority). Two numbers side by side, a number right before a date, and a number glued to an Arabic word start matching.

**Behaviour**
- *Not a phone* (`Offers` has no "a phone number"):
  - A digit run whose groups (split on space, dot or hyphen) are all 1–2 digits. This covers dotted times ("08.00 09.30 11.00"), a two-digit-year date plus a time ("05-12-26 10.30"), prices ("0.50 0.75 1.00") and hyphen slots ("08-30 09-30 10-30"), in Arabic and English text.
  - Existing non-phones stay: "10.30 11.30 12.30", dates with a 4-digit year plus a time, year lists, a 9-digit id without a trunk 0, mentions, digits in links, and the holdout-recall false-positive table.
  - Digits glued to a Latin letter before them, with or without a trunk 0 ("A1234567890123", "A0551234567", "ZX0551234567"), or with any letter or digit right after ("0551234567X"). These read as codes.
- *A phone*:
  - Each number in "0551234567 0559876543" (one space apart). The run must not swallow the second number and then look glued.
  - A number right before a date: "0551234567 12/5/2026", "0551234567 2026-05-12".
  - A number glued to an Arabic-script word before it: "واتساب0551234567", with Arabic-Indic digits too.
  - Existing phones keep firing: "055 123 4567", "055-123-4567", "966551234567", "+966 55 123 4567", "تواصل:0551234567,example.com".
- For every phone case: `Offers(e)[0] == "a phone number"`, exactly one `contact.advertised` signal, weight 1.0, not Known or Hard. The evidence has no part of the number (GO-003).

**Public surface:** `contact.Offers(*envelope.Envelope) []string`, `contact.New[P]()`, `contact.CodeAdvertised`. The tests also use the existing helpers `synth.Envelope` and `pipeline.NormalizeTexts`. No new exported names.

**Out of scope:** E.164 parsing or a default country (DESIGN §7.2 step 7); changing weights or `scoring.DefaultWeights().Version`; other contact channels; redactor (`redact.Text`) masking rules.

**DESIGN.md refs:** §7.1 (T0), §7.3, §7.5 item 2; DET-002; GO-003; docs/DETECTION-RULES.md (contact.advertised).

**Assumptions**
- A letter before the number is allowed only when it is Arabic script, because Arabic users often type "واتساب" straight into the number. A Latin letter before it marks a product, order or reference code, so "A0551234567" is not a phone. This is the conservative reading of "decide conservatively".
- "All groups 1–2 digits" also rejects a phone written in pairs ("05 51 23 45 67"). That miss is accepted, because a false positive costs more (DET-002).
- "0551234567 12/5/2026" already fires today. It stays in the table as a regression guard.

**Acceptance tests** (`acceptance/contact/phone_shapes_test.go`)
- TestPhoneShapesNotAPhone: no phone for dotted times, a two-digit-year date plus time, prices, hyphen slots (EN/AR); the existing non-phones; and Latin-letter codes or a trailing letter.
- TestPhoneShapesAreAPhone: a phone first in `Offers`, one signal of weight 1.0 that is not Known or Hard, and evidence without the number. Cases: two adjacent numbers (EN/AR), a number before a slash or ISO date, a number glued to an Arabic word (also with Arabic-Indic digits), and the existing phone shapes.
