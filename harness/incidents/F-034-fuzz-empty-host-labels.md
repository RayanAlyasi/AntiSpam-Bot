# F-034 bundle: fuzz-found canonicalization bug took two runs to close

Symptom: fuzz found non-idempotent URL canonicalization for empty host labels such as "http://..". A first fix left related variants; a second fuzz run found them.

Root cause: the fix handled the reported input instead of the class (empty labels produced by any dot sequence or by removal of invisible characters).

Evidence: builder report; no log file.

Prevention: after a fuzz failure, fix the invariant (canonical form is a fixed point; no empty labels), add several dot/label variants as seeds, and rerun fuzz for the full time budget before moving on.
