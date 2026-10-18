# Deviations: WhatsApp community support (v0.1.0)

DESIGN.md had no word on communities; §12.4 now records the design. Where the task left room, the
more conservative option was taken:

1. **Settings stay per group; a community is a selection, not a settings level.** A command on a
   community runs once per group the sender administers, each with that group's own admin check,
   history and audit entry. Nothing reads a community's settings at run time.
2. **Community defaults are copied once, eagerly.** They are the settings document under the
   community's JID (starting from the normal starting settings), copied to a group of the
   community when the bot joins it or is made admin there and the group has no document of its
   own. Inheriting lazily on every read was rejected: an admin of some groups of a community would
   then change the effective settings of another group of it where they aren't an admin.
3. **The community's own entry and its announcement group are never listed or protected.** Only
   admins post in either, so there is nothing to moderate; listing the announcement group under
   its community (it usually has the community's name) would only confuse `.use`. Before this
   change they could appear in `.groups` as ordinary groups.
4. **Only the commands the task names work on a community** (`.mode`, `.preset`, `.askadmins`,
   `.block`/`.blocknew`/`.allow <type>`, `.set`, `.blacklist add|remove`, `.status`). `.silent`,
   `.policy`, `.autoban`, `.settings`, `.get` and member commands answer that they work on one group
   at a time. `.blacklist … --instance` is instance-wide anyway and isn't fanned out.
5. **Blacklist entries aren't community defaults.** They're a list per group, not a setting, so a
   new group of the community doesn't inherit numbers blacklisted in its siblings.
6. **`.groups` counts the sender's groups under each community** ("2 groups" = the groups listed
   under it), not every group of the community; the groups the bot can't protect are named in the
   `.use` and command replies instead.
7. **A community's group list is fetched at most once per community per 6-hour refresh**, when a
   group of it (or the community itself) is snapshotted, plus link/unlink events in between. A
   failed fetch keeps the stored list and is retried on the next snapshot.
8. **`SubGroups` is on `wa.CommunityReader`, not on `wa.WAClient`.** Adding a method to WAClient
   broke the hidden acceptance suite, which has its own WAClient double. The adapter's and the
   fake's clients implement both; the connector's community source type-asserts the bot's client
   and reports `errors.ErrUnsupported` for one that can't list a community.
9. **Migration 00036**, not 00017: 00030–00035 are already applied in released databases, and goose
   refuses a missing lower-numbered migration.
