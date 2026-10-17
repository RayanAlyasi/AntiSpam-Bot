# Proposal: do not start a bundle before its dependencies are on staging

- Rule (for the /next-feature command instructions): Refuse to start a bundle whose dependent work packages are not merged to staging (or design it against interfaces only), because building on uncommitted overlays of sibling WIP forces removing and re-adding them at every merge and leaves a staged tree that does not build alone.
- Why: it cost WP-G several merge passes and left FK, blacklist and go.mod gaps.
- Incident: harness/incidents/WP-G-unlanded-sibling-overlays.md
