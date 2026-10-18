# Test-change request: let the normalizer strip strikethrough overlays

**Status:** doesn't block. The `feat/normalize-obfuscation` PR leaves the overlays out, so the
test passes as written.

## Test

`TestZalgoFloodStripped` in the hidden normalize suite. With U+0334–U+0338 in the set of
decorative marks that `normalize.Text` strips from letters, it fails:

```
--- FAIL: TestZalgoFloodStripped (0.00s)
    normalize_test.go:209: Text("c\u0334r\u0334y\u0334p\u0334t\u0334o\u0334").Stripped = 6, want 0 (1 combining marks per letter is below the flood threshold of 4 and is kept, DESIGN §7.2 step 2)
    normalize_test.go:209: Text("c\u0334\u0335r\u0334\u0335y\u0334\u0335p\u0334\u0335t\u0334\u0335o\u0334\u0335").Stripped = 12, want 0 (2 combining marks per letter is below the flood threshold of 4 and is kept, DESIGN §7.2 step 2)
    normalize_test.go:209: Text("c\u0334\u0335\u0336r\u0334\u0335\u0336y\u0334\u0335\u0336p\u0334\u0335\u0336t\u0334\u0335\u0336o\u0334\u0335\u0336").Stripped = 18, want 0 (3 combining marks per letter is below the flood threshold of 4 and is kept, DESIGN §7.2 step 2)
```

## Why it looks wrong

The test uses tilde and stroke overlays as its example of legitimate marks below the flood
threshold. Real text almost never uses them that way. Spam does: "text generator" strikethrough
(`s̶p̶a̶m̶`, U+0336) is the same trick as the per-letter underline (`س̲ڪ̲ل̲ي̲ف̲`, U+0332) found in
the maintainer's spam set. DESIGN §7.2 step 2 lists "combining-mark floods (Zalgo)" as
obfuscation to strip, and the Zalgo threshold exists to protect real orthography (Arabic, Hebrew,
IPA stacking up to three marks), not overlays.

As the test stands, `c̶r̶y̶p̶t̶o̶` doesn't match a `crypto` keyword and doesn't count as stripped
characters.

## What I believe is correct

Strip U+0334–U+0338 on letters and digits like the underline and overline marks (count them in
`Stripped`), and change the test's below-threshold example to marks that real orthography stacks,
for example `e\u0323\u0302` (Vietnamese ệ, decomposed) or Hebrew niqqud. NFKC already composes
`=\u0338` to `≠`, so math stays intact. Confidence: medium. IPA uses U+0334 (velarized ɫ), but
not in WhatsApp groups.

## What I tried

The normalize change ships with underline and overline marks only (U+0305, U+0331–U+0333,
U+033F, U+035E–U+035F). With that set, every hidden test passes. Adding U+0334–U+0338 fails
only this test.
