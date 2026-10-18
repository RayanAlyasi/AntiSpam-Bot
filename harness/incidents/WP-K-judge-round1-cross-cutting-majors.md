# WP-K: first judge review of every PR found real cross-cutting bugs

Symptom: majors in each PR, all passing the builder's own tests:
- non-admins could drive a review group
- posts into protected groups via `.set review.review_group` and via late votes
- overturned text survived in review rows
- the corpus received redacted text
- dedupe keys made repeat join rejections and re-ban kicks impossible
- stale blacklist marks
- only the SimHash was allow-listed on overturn, never media SHA-256 hashes (fixed in the wiring PR)

Root cause: each package was built against ports in isolation. Authorization, destination checks and data cleanup are cross-cutting and were checked per call site, not as invariants. Dedupe keys were written without asking what a legitimate repeat looks like.

Evidence: .agent/logs/judge-WP-K.md, .agent/logs/judge.jsonl.

Prevention: an acceptance checklist per bundle for who may call each entry point, where each output may be posted, and which derived fields exist for each identity of a message (text, SimHash, media hash, URL, number hash).
