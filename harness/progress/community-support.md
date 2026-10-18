# WhatsApp community support (v0.1.0)

Branch `feat/communities`, PR into `staging`. Built on the numbered `.groups` /
`.use <number>` from #54.

## Done

- `wa`: `GroupInfo.IsCommunity` / `IsAnnouncement`, `CommunityReader.SubGroups` (beside
  WAClient), `GroupEvent.Link` / `Unlink`. Adapter and connector convert whatsmeow's `IsParent`, `IsDefaultSubGroup`,
  `GetSubGroups` and link changes; the fake serves communities (`SetSubGroup`).
- Migration 00036: `groups.kind`, `community_jid`, `community_name`; `community_groups`.
- Tracker records each group's community on join, refresh and group events (link/unlink too),
  fetches a community's name and list at most once per 6 h, and calls a start hook on join or
  promotion; the connector wires it to `SettingsService.StartFrom` (community defaults).
- Chat: `.groups` with communities, `.use <community>`, one-reply fan-out of the settings
  commands and `.blacklist add|remove`, `.status` per group, unprotected groups named.
- Help (`.help`, `.help settings`) in English and Arabic; COMMANDS.md, GROUP_SETTINGS.md,
  DESIGN.md §12.4, §13.7, §14.

## Notes

- Branch `feat/communities` sits on staging 60b28d2 (#54, which brought the numbered groups and
  the ask-mode start), so the tamper audit's merge-base is current.
- `TestTextLinearOnHostileInput` (normalize) failed once under load in check-fast and passes alone.

## Follow-ups

- docs/COMMANDS.md lists the Arabic alias of `.groups` as `.مجموعات`; locales/ar.yaml has
  `المجموعات`.
- A community renamed while the bot isn't tracking the community's own row keeps the old name on
  its groups until their next 6-hourly refresh.
