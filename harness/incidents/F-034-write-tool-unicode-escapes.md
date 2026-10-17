# F-034 bundle: Write/Edit tools decode \uXXXX escapes into Go source

Symptom: Go source for the normalizer contained a literal BOM and invisible characters where the code meant to have escape sequences, hiding behaviour from review and risking silent breakage.

Root cause: the Write/Edit tools decode \uXXXX escapes in content into literal characters. Normalization code and tests are full of such characters.

Evidence: builder report for F-034 (no log file); worked around by a re-escaping script after every write.

Prevention: a rule that invisible or bidi characters are never written literally; after writing, scan the file for such code points (BOM, ZWSP, bidi controls) and re-escape. A `make check-fast` step that fails on invisible characters in .go files would catch this mechanically.
