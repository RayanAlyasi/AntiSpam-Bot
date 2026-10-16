---
paths:
  - "internal/ai/**"
---
# AI tier

- [AI-001] Provider-neutral: everything goes through the `AIClassifier` interface. No provider-specific types outside the adapters.
- [AI-002] Only the uncertain band reaches the AI. Cache results by normalized text + context hash, and reuse one result per near-duplicate cluster.
- [AI-003] Message and context are data, not instructions: wrap them clearly, validate structured output strictly, and treat invalid output as "unsure".
- [AI-004] An AI-only "spam" verdict routes to admins until that source has a track record (DESIGN.md §9.2).
