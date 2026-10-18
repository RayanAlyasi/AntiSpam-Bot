# Deviations: hold-out recall (related corpus wording plus contact channels)

1. **Clitics: article first, then a lone و.** The brief said "drop one leading clitic", which
   would give `واجبات` (root waw) and `الواجبات` different terms. Stripping one article form and
   then one lone و (each only when 3 letters remain) makes all the variants one term, at the cost
   of merging words that differ only by a leading و. It only affects the weak related level.
2. **Words that describe a link are noise terms** (link, group, join, channel, click, `رابط`,
   `قروب`, `جروب`, `مجموعة`, `انضم`, `قناة`), and the channel names in both languages (whatsapp,
   telegram, `واتساب`, `واتس`, `تلغرام`). They accompany any invite link and the contact
   analyzer already counts the link, so counting them again tied ordinary "here's the group link"
   messages to invite spam. Chosen over a shared-term floor, which contradicted the contract.
3. **The import planner keeps the plain fingerprint.** Dedupe and the hold-out clustering stay at
   the CERTAIN and loose bands, so the hold-out composition and earlier imports don't change; only
   the live index and the recall replay use match terms.
4. **Zero related limits turn the level off**, so a `Thresholds` literal written before this
   change never yields Related.
5. **A national number must be phone-shaped.** The contract lists "a national number in a text
   surface that the redactor would mask", but the redactor masks any 9+ digits, dates and year
   lists included. Only a run with a leading + or 0, or 11–15 digits written together, counts, and
   date-shaped groups never do (judge round 1).
