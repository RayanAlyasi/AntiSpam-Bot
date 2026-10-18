# Proposal: coordinator assigns one owner to each shared port

Rule: when parallel bundles both need an interface (for example review.Corpus), the plan names one owner for the port and its table, and the other bundle must reuse it after checking staging.

Reason: wp-k and wp-l each built a review.Corpus and a table, leaving two parallel schemas to reconcile.

File: `docs/AGENTIC_ENVIRONMENT.md` or the planning rules.

Incident: harness/incidents/F-088-duplicate-review-corpus-implementations.md
