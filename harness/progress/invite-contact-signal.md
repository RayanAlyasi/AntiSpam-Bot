# Order-in-private contact: findings (fast path, post-v0.1.0)

Date: 2026-10-18. Branch `feat/invite-contact-signal`. Fast-path fix asked for by the maintainer
(no /next-feature, no contract). Aggregates only: the dataset stays outside the repo.

## The miss

A sick-leave ad with a `chat.whatsapp.com` invite, sick-note wording and "order through the group
admin only" went undetected live. On staging, a group invite link already counts as a contact
(`offerInvite`), and synthetic look-alikes with an invite reach T_review (corpus.related +
contact.advertised). So the live miss had either too little shared wording or ran a build without
PR #65. What staging did miss is the same ad with no number or link at all: "للطلب خاص", "التواصل
معنا", "order through the group admin".

## What was built

`contact.advertised` gets a seventh kind, "a request to order or ask in private"
(`internal/detect/contact/private.go`). The word rules are adjacency-based after four judge
rounds: a call form (للطلب, للتواصل, اطلب, …) followed by a private or admin word; a noun
(الطلب, استفسار, orders, …) only with a via word right after it (عن طريق, عبر, على, via, through);
bare "خاص" only right after a call; "المشرف" only before a group word; "اطلب من X" is "ask X".

## Results (dry run against an empty Postgres, no AI)

| | before | after |
|---|---|---|
| Operator's hold-out (68) | 54 to admins (79%) | 56 to admins (82%) |
| Default random 20% split (39) | 25 to admins (64%) | 27 to admins (69%) |
| 158 real ordinary messages + 159 synthetic hard negatives | 0 flagged | 0 flagged |

The phrase alone fires on 0/158 real ordinary messages, 1/159 synthetic hard negatives and 37/320
corpus spam texts.

## Follow-ups (judge round 4 minors)

- Member warnings that quote the phrase ("انتبهوا من التواصل على الخاص مع اللي ينشرون …") reach
  T_review with corpus.related. Skipping an ask word right after a warning word (يمنع، ممنوع، عدم،
  لا، احذروا، من) would fix the explicit ones; plain descriptions are the AI's job.
- After a call word, the admin may sit anywhere in the 4-word window: "اطلب الرابط من الادمن"
  ("ask the admin for the link") fires. Treat "اطلب + object + من" as "ask X".
- "send me a dm to order the pizza" fires.
- A noun followed by "على" fires when "على" isn't "on (the channel)": "تواصل على طول مع الادمن".
- Recall gaps left on purpose: "الإدارة" (also a university's administration) and confusable
  Latin letters ("Ꭰ𝖬 to order"); matching uses Norm, not the skeleton.
- 9-digit national numbers without a leading 0 ("واتس 5xxxxxxxx") are missed; that is PR #67's area.
