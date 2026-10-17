# Deviations from docs/DESIGN.md

Where the build knowingly differs from the design, or picked an interpretation of an ambiguous section. One entry each: date, feature, DESIGN.md section, what was done instead, and why.

## 2026-10-17 · F-000 · §4, §5.3, §2.3

- **Own JID type.** `wa.JID` is a string type (`"<user>@<server>"`), not whatsmeow's `types.JID`, so code outside `internal/wa` never imports whatsmeow (GO-004). The adapter converts.
- **Adapter and envelope builder live under `internal/wa/adapter/`.** DESIGN.md §4 has the Normalizer build the envelope from protos, but only `internal/wa` may import whatsmeow; so `internal/envelope` holds only the types, and the unwrapper and proto-to-envelope builder go in `internal/wa/adapter/...`. The adapter is a sub-package so importing `wa` (for `JID`) doesn't link whatsmeow into domain packages.
- **`ParticipantAction` has only `ParticipantRemove`.** The bot never adds people (unsolicited, §9.6) or changes roles, so the interface doesn't offer it. `SetGroupMemberAddMode`, business profile and IsOnWhatsApp are left out of `WAClient` until a feature needs them (more conservative surface).
- **Test Postgres is `postgres:18-alpine`** per the initializer brief; DESIGN.md §14/§19.1 say `postgres:16` for deployments. The deployment compose file (F-091) should follow §19.1 unless the human decides otherwise.

## 2026-10-17 · F-000 · §20, §21, §7.5, §19 (milestones, user decision)

- **v0.1 is released before DESIGN.md §20's "before enforcing anywhere" gates.** The user wants a quick, stable first release, so v0.1 ships without the hold-out recall report (F-151) and the shadow-precision report (F-153). F-100's docs tell deployers to run new groups in shadow mode first.
- **The learning system leaves the first version** (§21 puts it in Phase 1):
  - v0.1 keeps only the corpus of admin-confirmed spam (F-113), exact and near-duplicate matching against it, and retraction on overturn (F-118);
  - auto-fingerprints, the poisoning guards (per-admin limits and admin reputation F-142, recent-traffic check F-143), few-shot examples and dataset import move to v0.2.
  
  The risk is lower than with full learning: in v0.1, only an admin's explicit `.spam` or review ✅ adds anything, and an overturn removes it.
- **Release features go beyond §19.** These were added from the user's release research and aren't in DESIGN.md §19, which only says images are multi-arch and signed and that migrations run on start (§19.3):
  - edge images from staging (F-093);
  - SemVer and branch rules for tags (F-092);
  - a weekly whatsmeow bump PR (F-098);
  - refusing to start on a newer schema (F-099);
  - the Conventional Commits workflow: release-please (F-094), PR title lint (F-095), a docs drift check (F-096) and a main → staging sync after releases (F-097).
