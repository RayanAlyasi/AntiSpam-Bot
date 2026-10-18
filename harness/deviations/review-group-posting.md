# Deviations: posting in a review group that was once protected (v0.1.0 fix)

1. **A review group is open for posting unless the bot is active there.** A group named in some
   group's `review.review_group` gets posts even when it has a settings document of its own,
   left from when the bot was admin there. The exception is a group whose `app.groups.status`
   is `active`: the bot moderates it again, it is protected, and DESIGN §9.5 says a protected
   group can't be a review group, so it stays silent (§9.7). This matches the chat handler,
   which treats only `active` groups as protected, and `.reviewgroup here`, which is accepted
   only outside them.
2. **`pending_admin` and `left` don't change anything for other groups.** A group with a
   settings document that isn't a review group stays protected and silent whatever its status,
   as before. Only the review-group check looks at the status.
3. **A group can't be its own review group.** A settings document that names its own group as
   the review group doesn't open that group for posting; it stays protected and silent.
4. **A review group gets no shadow reactions.** If it still has an old shadow-mode document with
   `shadow.reactions` on, the queue now refuses shadow reactions there, because the group is no
   longer treated as protected. That is the quieter direction.
