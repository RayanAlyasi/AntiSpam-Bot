# WP-I: SettingsService.Save replay and stale cache (F-054)

Symptom: judge FAIL on PR #27: Save replayed changed keys one at a time (preset switches did not round-trip), and a load racing a Save could write a stale document into the cache.
Root cause: Save was built on Set per key rather than merging onto the latest stored tree and parsing once; the cache write after a store read did not check whether an invalidation happened meanwhile.
Evidence: .agent/logs/judge-F-054.md (scratch tests in .agent/scratch/F-054/); fix in 59167ec / 0d726bb.
Prevention: for read-through caches, capture a generation before the store read and skip the write if it moved or a higher version is cached. For multi-key updates, merge then validate once. Write a concurrent load-vs-save test up front.
