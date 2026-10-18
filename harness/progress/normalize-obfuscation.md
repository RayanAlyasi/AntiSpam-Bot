# Arabic spam obfuscation in the normalizer (v0.1.0)

Branch `feat/normalize-obfuscation`, PR into `staging`.

## Done

- `StripDecorations` (`internal/normalize/decoration.go`): underline and overline marks
  (U+0305, U+0331–U+0333, U+033F, U+035E–U+035F) are removed in `Fold` whatever their base and
  counted in `Stripped`; the ones NFKC makes from ‾ and ‗ are removed uncounted.
- `text.invisible_chars` evidence now says "hidden or decorative characters".
- `FoldArabic` folds ڪ ګ ڬ to ك, like ک.
- `StripWordSeparators` (`arabic.go`): a run of tatweel and `. · • - _ | /` between two Arabic
  letters, with at least one of each (tashkeel allowed inside), is removed in `matchForm`
  before tatweel goes; its separators are counted in `Stripped`.
- New tests: `internal/normalize/obfuscation_test.go` (table test, linearity), two `FuzzText`
  seeds in `internal/normalize/testdata/fuzz/FuzzText/`.
- Docs: DESIGN §7.2 steps 2 and 5, docs/DETECTION-RULES.md (patterns).

## Follow-ups

- Strikethrough overlays (U+0334–U+0338) below the flood threshold are kept, as
  `TestZalgoFloodStripped` requires; a test-change request asks whether to strip them.
- Look-alike separators (، ٭ – *) wedged with tatweel are not removed.
- Wedged separators without tatweel (`اج.ازه`) are not caught; see the deviation file.
