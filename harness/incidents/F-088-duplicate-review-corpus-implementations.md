# F-088 bundle: two review.Corpus implementations after wp-k merged

- Symptom: after merging #39, `internal/review/pgstore/wiring.go` (tables from 00033_corpus_sources) and `internal/app/fastlane/learning.go` (00034_corpus_messages) both implement review.Corpus on separate tables. Production wires fastlane.CorpusLearning. Judge round 4 raised it as a minor.
- Root cause: wp-k and wp-l ran in parallel and both needed the corpus port; no one owned it, so each built an adapter and a table. Not caught until the merge.
- Evidence: `.agent/logs/judge-F-088.md` round 4, commit 7a76374 and 5a3b16b (follow-up recorded).
- Would have prevented: the coordinator naming one owner for a shared port and its table when planning parallel bundles, and the later bundle checking staging for an existing implementation of the interface before writing one.
