# Drop F-096 (docs drift check)

**Date:** 2026-10-17 · **Decided by:** maintainer

F-096, the docs-check agent after merges into staging, is removed from v0.1. User-facing docs are updated in the same PR as the change they describe, and the judge treats a missing update as a major finding (CLAUDE.md, "Commits and pull requests"). A separate agent run on every merge would add cost, an API key secret and a PR-creation setting for little extra safety. See docs/DESIGN.md §23.11.
