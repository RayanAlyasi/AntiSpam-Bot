# holdout-recall: neardup.Key skeleton rewrites digits and alef, so term cleaning went wrong

Symptom: terms derived from the key were wrong; 12 became l2 and alef became Latin l, so numeric and Arabic words were mis-cleaned.

Root cause: the confusables skeleton in neardup.Key maps digits to letters and alef to Latin l. Cleaning after the key loses the original character classes.

Evidence: commit c595b99 (internal/detect/neardup/terms.go, neardup.go doc comment), harness/progress/holdout-recall.md.

Prevention: treat Key output as for equality only. Clean terms per word on the normalized text, then compute the key. Say so in the Key doc comment.
