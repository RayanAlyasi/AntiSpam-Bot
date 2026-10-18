# holdout-recall: shared-term floor of 4 contradicted the contract's Jaccard rule

Symptom: a floor of 4 shared terms for Related fixed two hard-negative false positives but broke the contract's Jaccard rule on short texts.

Root cause: the fix targeted the threshold, not the noise source. The false positives came from link-describing words counted as terms.

Evidence: commit c595b99, harness/deviations/holdout-recall.md.

Prevention: when a false positive needs a threshold change that conflicts with the contract, filter the noise terms instead (done: link-describing words became noise terms).
