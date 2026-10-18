# WhatsApp Anti-Spam Bot: Design Document

**Status:** Draft for review (revision 12: release automation specified (release-please, GoReleaser with Immutable Releases, release App, back-merge, PR title lint; no docs agent); revision 11: v0.1 milestone first, and the release and versioning plan in §23; revision 10: tech stack decided (Go modular monolith, Postgres only, River), no embeddings in the core, frontend decided later; revision 9: context bundle for the AI, explicit AI cost controls, provider-neutral AI classifier; revision 8: spam-only bootstrap, no collection of normal messages, certainty routing as the core flow; revision 7: generic labeled-dataset import replaces the old-database import, and a manual phone-number blacklist; revision 6: AI and learning in the first version; revision 5: new enforcement model with instant delete + ban by default, no trust grace on detection, silent bot, auto-ban matrix and ask-admins mode; revision 4: adds hacked-account handling, one-number capacity model, unbounded queue with slow-down-only pacing and capped kick batches, and the open-source/self-hosting framing to revision 3's network bans and spammer intelligence). Nothing is built yet.
**Stack:** Go + [whatsmeow](https://github.com/tulir/whatsmeow) (`go.mau.fi/whatsmeow`), PostgreSQL, Docker Compose (ARM64 + AMD64); runs anywhere Docker does (the user's own instance runs on Coolify).
**Scope:** A from-scratch rewrite. The existing Baileys/Node code in this repo is ignored.
**Project type:** **open source and self-hosted**, aimed at big groups and communities. Anyone can run their own instance with one `docker compose up`. Throughout this document, **"operator" means whoever deploys an instance**, and "global" means "across all groups of that one deployment".

---

## Table of contents

1. [Goals and non-goals](#1-goals-and-non-goals)
2. [whatsmeow capability audit (verified against source)](#2-whatsmeow-capability-audit)
3. [Hard constraints and risks that shape the design](#3-hard-constraints-and-risks)
4. [Architecture overview](#4-architecture-overview)
5. [Ingestion and the Content Envelope](#5-ingestion-and-the-content-envelope)
6. [Analyzers for every content type](#6-analyzers-for-every-content-type)
7. [Tiered detection pipeline and scoring](#7-tiered-detection-pipeline-and-scoring)
8. [Avoiding false positives](#8-avoiding-false-positives)
9. [Enforcement: bans, the auto-ban matrix, reviews, raids, silence](#9-enforcement)
10. [Outbound action queue](#10-outbound-action-queue)
11. [Caching](#11-caching)
12. [Per-group settings](#12-per-group-settings)
13. [Web dashboard, auth and onboarding](#13-web-dashboard-auth-and-onboarding)
14. [Data model](#14-data-model)
15. [Scalability and multiple bot numbers](#15-scalability-and-multiple-bot-numbers)
16. [Observability](#16-observability)
17. [Security and privacy](#17-security-and-privacy)
18. [Testing strategy](#18-testing-strategy)
19. [Deployment and self-hosting](#19-deployment-and-self-hosting)
20. [Phased roadmap](#20-phased-roadmap)
21. [Open questions and trade-offs](#21-open-questions-and-trade-offs)
22. [Open source: licensing and contributions](#22-open-source-licensing-and-contributions)
23. [Releases and versioning](#23-releases-and-versioning)

---

## 1. Goals and non-goals

### Goals

| Goal | What it means in practice |
|---|---|
| **Catch clever spam** | Every message type is analyzed, including contact cards, business vCards, stickers, channel shares, polls, events, locations, edits, Status content shared, quoted or mentioned in the group, Group Status posts, and the sender's own profile (name, about text, picture, business profile, own status when visible). It also catches campaigns that span groups. |
| **Few false positives** | Every action comes from a confidence score with explanations. Uncertain cases go to admins chosen per group, who decide with a reaction or a one-word reply. Spam is deleted the same way for everyone, but established members, possibly hacked accounts and anything the group marks `ask` are never banned automatically; admins decide. Auto-bans need strong evidence (per content type and evidence source), and there's a shadow mode, an always-ask-admins mode and an appeal path. |
| **Fast** | Clear-cut spam is deleted within about 1–3 s of arrival. The cheap tiers take under 5 ms CPU per message. AI is used only for the ambiguous few percent. |
| **Safe for the bot's account** | All WhatsApp writes go through one paced queue per bot number, with jitter, rate limits, backoff and a kill switch. The bot never sends unsolicited bulk DMs. |
| **Flexible per group** | A per-type auto-ban matrix, always-ask-admins mode, content-type rules (rule-breaking is deleted silently, not banned), domain lists, allow-lists, an optional warn/mute ladder, all set per group. |
| **Open source, easy to self-host** | One Docker Compose file, documented env/config with sensible defaults, a step-by-step setup guide, ARM64 + AMD64 images. Each deployment is independent; nothing is shared between instances unless the operator opts in (§15). |
| **Easy for non-technical admins** | Add the bot, make it admin, and it works with safe defaults. A phone-number login with a WhatsApp OTP gives access to the dashboard. In-chat commands work for admins who never open the dashboard. |
| **One number is enough** | A single bot number and a small VM comfortably protect many large groups and communities. CPU is not the limit; WhatsApp write pacing is. The rate has a fixed, conservative ceiling that never ramps up on its own, so the design copes with big waves through early lockdown, batching and newest-spam-first. See the capacity model in §15. More numbers and nodes are optional (Phase 6) and need no redesign. |

### Non-goals (for now)

- Moderating the bot's own 1:1 chats. DMs to the bot are only used for login, appeals, admin reviews and commands.
- Moderating channels/newsletters as a channel owner. Channel *shares and invites* posted into groups **are** in scope.

- Calls (whatsmeow can't handle calls, and they aren't needed).
- Being an official WhatsApp Business API product. This is an unofficial multi-device client. See [§3](#3-hard-constraints-and-risks).

**In scope, unlike the first draft:** Status and broadcast content wherever it reaches a protected group or tells us something about a sender's profile. That includes status shares, forwards and replies, status mentions of the group, Group Status posts, the sender's own status (best effort), and broadcast-list content forwarded into groups (§2.1, §6.18–6.20).

---

## 2. whatsmeow capability audit

I verified this against the whatsmeow source at commit `0b0a1cd` (2026-10-16; the repo is very actively maintained, with commits daily). The module requires **Go 1.26** (`go.mod`: `go 1.26.0`, toolchain `go1.27.2`).

Legend: ✅ supported directly · ⚠️ supported with caveats · 🔧 needs a workaround · ❌ not possible

### 2.1 Receiving messages

| Capability | Status | How | Notes |
|---|---|---|---|
| All incoming group messages | ✅ | `*events.Message` via `Client.AddEventHandler` | `Info` (`types.MessageInfo`) has `Chat`, `Sender`, `SenderAlt`, `AddressingMode` (pn/lid), `PushName`, `Timestamp`, `Edit`, `VerifiedName`, `MediaType`, `Type`. |
| Wrapper unwrapping | ⚠️ | `events.Message.UnwrapRaw()` already strips DeviceSent, Ephemeral, ViewOnce v1/v2/v2ext, LottieSticker, DocumentWithCaption, EditedMessage, BotInvoke | **Not** unwrapped: `GroupMentionedMessage`, `SpoilerMessage`, `AssociatedChildMessage`, `StatusMentionMessage` and other `FutureProofMessage` wrappers. **We need our own recursive unwrapper** (§5.2). |
| Text | ✅ | `Message.Conversation`, `ExtendedTextMessage.Text` | `ExtendedTextMessage` also carries the link preview: `MatchedText`, `Title`, `Description`, `JPEGThumbnail`, and `InviteLinkGroupType*` for group-invite previews. |
| Images / video / audio / PTV / documents | ✅ | `ImageMessage`, `VideoMessage`, `PtvMessage`, `AudioMessage`, `DocumentMessage` | Each has `FileSHA256` (a free exact fingerprint, no download needed), `Mimetype`, `FileLength`, `Caption`, and often an inline `JPEGThumbnail`. Documents also have `FileName`, `Title` and `PageCount`. |
| Stickers | ✅ | `StickerMessage` | `FileSHA256`, `IsAnimated`, `IsLottie`, `IsAvatar`, `IsAiSticker`, `PngThumbnail`, `AccessibilityLabel`, `Emojis`. Sticker packs: `StickerPackMessage` (`Name`, `Publisher`, `PackDescription`, `Caption`) and `Client.FetchStickerPack(packID)`. |
| Contact cards | ✅ | `ContactMessage{DisplayName, Vcard}`, `ContactsArrayMessage{DisplayName, Contacts[]}`, `DocumentMessage.ContactVcard` | The vCard is raw text. Business contacts usually carry `X-WA-BIZ-NAME` / `X-WA-BIZ-DESCRIPTION` and `TEL;waid=` lines (to confirm in the Phase 0 spike). We parse the vCard ourselves. |
| Business accounts (sender) | ⚠️ | `MessageInfo.VerifiedName` (certificate + details) on messages from businesses; `Client.GetBusinessProfile(jid)` | **Caveat:** whatsmeow's `parseBusinessProfile` only parses address, email, categories, hours and `profile_options`. It does **not** parse the business *description* or *website*. Fix: a small upstream PR, or a raw IQ through `DangerousInternals()` that reads those child nodes. Small effort, but needed for spam in business descriptions. |
| Channel/newsletter shares | ✅ | Forwarded channel posts: `ContextInfo.ForwardedNewsletterMessageInfo{NewsletterJID, NewsletterName, ...}`. Channel invites: `NewsletterFollowerInviteMessageV2{NewsletterJID, NewsletterName, Caption, JPEGThumbnail}`. Links: `whatsapp.com/channel/<code>` | Enrichment: `GetNewsletterInfo(jid)` and `GetNewsletterInfoWithInvite(code)` return name, **description**, subscriber count, verification state and picture. |
| Group invites | ✅ | `GroupInviteMessage{GroupJID, InviteCode, GroupName, Caption}`; links `chat.whatsapp.com/<code>` | `GetGroupInfoFromLink(code)` / `GetGroupInfoFromInvite(...)` give the name, topic, size and creator **without joining**. These are IQ calls, so they're rate-limited and cached. |
| Other WhatsApp links | ✅ | `wa.me/message/<code>` → `ResolveBusinessMessageLink`; `wa.me/qr/<code>` → `ResolveContactQRLink`; `wa.me/<phone>` → `IsOnWhatsApp` + `GetUserInfo` | Resolves "DM me" links to a business name and pre-filled text. |
| Polls | ✅ | `PollCreationMessage` (+V2/V3/V5/V6; V4 is a FutureProof wrapper) | `Name` and `Options[].OptionName` are plain text. Votes are encrypted; we don't need them. |
| Locations | ✅ | `LocationMessage{Name, Address, URL, Comment}`, `LiveLocationMessage` | The URL and Comment fields can hide spam. |
| Events | ✅ | `EventMessage{Name, Description, Location, JoinLink}` | A newer spam vector: an "event" with a call link or URL. |
| Commerce | ✅ | `ProductMessage.ProductSnapshot{Title, Description, URL, PriceAmount1000}`, `OrderMessage`, `RequestPaymentMessage`, `Buttons/List/Template/InteractiveMessage` | Each text field goes to the text analyzer. |
| Edits | ✅ | `Info.Edit == "1"` plus a `ProtocolMessage{Type: MESSAGE_EDIT, Key.ID, EditedMessage}` (inside an `EditedMessage` wrapper) | **Important attack:** a member posts something benign, then edits it into spam. We re-analyze edits against the *original message ID*. (`ParseWebMessage` rewrites this for history sync; the live path leaves the ProtocolMessage for us to handle.) |
| Deletes by sender | ✅ | `ProtocolMessage{Type: REVOKE}`, `Info.Edit` = `"7"` (sender) or `"8"` (admin) | Used to cancel pending actions and log evasion ("post, then delete fast"). |
| View-once | ❌ content / ✅ metadata | Linked devices **don't receive** view-once media. whatsmeow emits `*events.UndecryptableMessage` with `UnavailableType == "view_once"` | We can't see the content, but we know *who* sent a view-once and the message ID, so we **can still delete it**. Policy: an optional "block view-once from new/untrusted members" setting. |
| Undecryptable messages | ⚠️ | `*events.UndecryptableMessage` | whatsmeow retries automatically. If a message stays undecryptable we know the sender and ID only. |
| Reactions | ✅ | `ReactionMessage{Key, Text}` (plain in groups and DMs); `EncReactionMessage` + `Client.DecryptReaction` for encrypted variants | Two uses: reaction-flood detection, and **admin review decisions** (§9.5). `Key.ID` points at the reacted-to message, so we can match it to our review message. An empty `Text` means the reaction was removed. |
| Status shared/forwarded into a group | ✅ (to confirm in spike) | `ContextInfo.ForwardOrigin == STATUS`, `ContextInfo.StatusSourceType` (IMAGE/VIDEO/GIF/AUDIO/TEXT/MUSIC), `ContextInfo.StatusAttributionType` (RESHARED_FROM_MENTION / _POST / _POST_MANY_TIMES), `ContextInfo.StatusAttributions[]` | Each `waStatusAttributions.StatusAttribution` can carry an **`ActionURL`**, an `ExternalShare{ActionURL, ActionFallbackURL}`, a `StatusReshare.Metadata{ChannelJID, HasMultipleReshares}` (a status re-shared from a *channel*) or a `GroupStatus{AuthorJID}`. All of these feed the URL, channel and campaign analyzers. |
| Replies quoting someone's status | ✅ | `ContextInfo.RemoteJID == "status@broadcast"` with `QuotedMessage` (the quoted status content) and `Participant` (the status author); `StatusQuotedMessage{Text, Thumbnail, OriginalStatusID}` for question/answer statuses | The **quoted status content is analyzed too**. A classic trick is a harmless reply to a spammy status, which puts the spam (the quoted text and thumbnail) in front of the whole group. |
| Status mentions of the group | ✅ (to confirm in spike) | `StatusMentionMessage`, `GroupStatusMentionMessage` (both `FutureProofMessage` wrappers); `StatusNotificationMessage{Type: STATUS_RESHARE / STATUS_GROUP_STATUS_REPLY / ...}` | `UnwrapRaw` doesn't unwrap these; our recursive unwrapper does (§5.2). The wrapped message is the status content, which we analyze like any other message. |
| Group Status posts | ✅ (to confirm in spike) | `GroupStatusMessage`, `GroupStatusMessageV2` (`FutureProofMessage` wrappers), `ContextInfo.IsGroupStatus` | whatsmeow has **no dedicated handling** for these (no references outside the protos), so they arrive as ordinary messages in the group chat with a wrapper. Same recursive unwrapping, then normal analysis. They can be deleted with the normal admin revoke (to confirm). |
| The sender's own Status posts | ⚠️ best effort | Messages with `Info.Chat == types.StatusBroadcastJID` (`status@broadcast`), sender = author | **Limitation:** WhatsApp sends status updates only to people the author shares status with. By default that's the author's *contacts*, so the bot sees a member's status only if that member has the bot's number saved and doesn't exclude it. Most spammers won't. Treat this as an occasional extra profile signal, never something the design depends on. |
| Broadcast lists | ⚠️ limited | When the bot itself is on someone's broadcast list: `Info.Chat` is the list JID (`<id>@broadcast`), `MessageSource.IsIncomingBroadcast()` is true, and `BroadcastListOwner` is filled. `BroadcastRecipients` is only filled for lists *we* own. | **What we can't see:** recipients never see the list *name* or other recipients. A broadcast message that someone forwards into a group is indistinguishable from any other forward: we see only `IsForwarded` / `ForwardingScore` / `ForwardOrigin`. Use: if a member sends the bot broadcast spam in DM, that's a strong reputation signal for that person (they're mass-messaging strangers). |
| Unknown/new types | ✅ (via reflection) | Any field of the `waE2E.Message` proto | We walk the proto with `protoreflect` and pull out every string field, so new message types still get text analysis (§6.13). |

### 2.2 Media and profiles

| Capability | Status | How | Notes |
|---|---|---|---|
| Download media | ✅ | `Client.Download(ctx, msg)` / `DownloadToFile` (streams to disk), `DownloadThumbnail` | Media expires on WhatsApp's CDN after some time; `SendMediaRetryReceipt` can re-request it. Inline thumbnails (`JPEGThumbnail`, `PngThumbnail`) are often enough to hash **without downloading at all**. |
| Profile pictures | ⚠️ | `GetProfilePictureInfo(jid, &GetProfilePictureParams{Preview, ExistingID, CommonGID})` returns a plain HTTPS URL | `ExistingID` returns nil if unchanged, which is cheap. Privacy-restricted pictures may fail; `CommonGID` (a group we share) helps. `*events.Picture` signals changes. |
| About/status text, devices, LID | ✅ | `GetUserInfo([]jid)` → `Status`, `PictureID`, `VerifiedName`, `Devices`, `LID` | Batched (takes a slice), so one IQ covers many users. |
| Contact names | ✅ | `Store.Contacts.GetContact` → `PushName`, `BusinessName`, `RedactedPhone` | Push names come with every message (`Info.PushName`). |
| Newsletter metadata | ✅ | `GetNewsletterInfo`, `GetNewsletterInfoWithInvite` | Name, description, subscriber count, verification, picture. |

### 2.3 Group administration (requires the bot to be a group admin)

| Action | Status | API |
|---|---|---|
| Delete any member's message for everyone | ✅ | `SendMessage(chat, cli.BuildRevoke(chat, senderJID, msgID))` (`RevokeMessage` is deprecated). This is sent as a group message. |
| Remove participants (batched) | ✅ | `UpdateGroupParticipants(group, []JID, ParticipantChangeRemove)` takes **many JIDs in one call**. We cap the batch size (§10.4) until the spike shows what's safe. |
| Approve/reject join requests | ✅ | `GetGroupRequestParticipants`, `UpdateGroupRequestParticipants(..., approve/reject)` |
| Join-request *notifications* | 🔧 | Not parsed into a typed event. They arrive as `events.GroupInfo.UnknownChanges` (raw `waBinary.Node`). We parse that node, or poll `GetGroupRequestParticipants` for groups with approval mode on. |
| Lock group / admins-only messages | ✅ | `SetGroupAnnounce`, `SetGroupLocked`, `SetGroupJoinApprovalMode`, `SetGroupMemberAddMode` |
| Reset invite link | ✅ | `GetGroupInviteLink(group, reset=true)` |
| Group info, participants | ✅ | `GetGroupInfo`, `GetJoinedGroups`. `GroupParticipant{JID, PhoneNumber, LID, IsAdmin, IsSuperAdmin}` |
| Membership/admin changes | ✅ | `*events.GroupInfo{Join, Leave, Promote, Demote, JoinReason, Sender, ...}`, `*events.JoinedGroup` |
| Per-member mute | ❌ | WhatsApp has no such feature. Emulated by the bot deleting the member's messages (§9.3). |
| Act on other admins | ❌ | The bot can't delete messages from, or remove, the group owner/superadmin. In practice we also never act on admins (policy). |

### 2.4 Sessions, stores and connection

| Topic | Finding |
|---|---|
| Store | `store/sqlstore.Container` works on **PostgreSQL** (pgx) or SQLite; **we use Postgres only** (§4.1). `Container.GetAllDevices()` / `NewDevice()` manage **multiple bot numbers in one database**. |
| Pairing | QR (`GetQRChannel`) or **phone-number pair code** (`PairPhone`), which is nicer for headless servers. |
| Event delivery | Handlers run **synchronously** on the receive loop. `AddEventHandlerWithSuccessStatus` and `SynchronousAck` let us ack a message only after we have durably stored it. Handlers must return fast, so we hand work off to a queue. |
| Reconnect | `EnableAutoReconnect` (default on), `AutoReconnectHook`, `KeepAliveTimeout`/`KeepAliveRestored` events. |
| Ban/limit signals | `*events.TemporaryBan{Code, Expire}` (codes: 101 sent to too many strangers, 102 blocked by users, 104 same message to too many…), `*events.LoggedOut`, `*events.StreamReplaced` (two processes on one session), `ErrIQRateOverLimit` (429). |
| LID addressing | Groups increasingly use **LIDs** (privacy IDs) instead of phone numbers. `MessageInfo.SenderAlt` / `GroupParticipant.PhoneNumber` / `Store.LIDs.GetPNForLID` map between them, but a phone number isn't always known. **We key people by LID and add the PN whenever we learn it** (§7.7). |

---

## 3. Hard constraints and risks

These shape everything below.

1. **Unofficial client, so ToS and ban risk.** whatsmeow speaks the WhatsApp Web multi-device protocol. Meta can ban the bot number, which is the single biggest operational risk. Mitigations: a paced action queue (§10); **no unsolicited DMs to strangers** (temp-ban code 101 is exactly that); no identical bulk messages (code 104); an aged, real SIM number; and a **spare number** ready, with group re-onboarding automated.
2. **The bot must be a group admin** to delete or remove anything. Onboarding is built around that (§13.6).
3. **No pre-moderation.** WhatsApp delivers messages to all members instantly, so the bot can only *react*. The "hold for review" queues you see on Discord/Telegram are impossible. That's why deletion is fast, and why **irreversible actions (kick) need higher confidence than reversible ones (delete)**.
4. **Deletion can't be undone.** An appeal can restore membership and reputation, but not the deleted message. So we keep an *evidence snapshot* for every deletion (for admins to review; deleted on overturn, so no normal text is kept) and offer shadow mode before going live.
5. **No native mute.** The default enforcement is delete + ban, so mute only exists with the optional ladder (§9.3). It's emulated by deleting messages, which costs write budget. Lock-down mode (`SetGroupAnnounce`) is the bulk alternative during raids.
6. **Admin-delete time window.** WhatsApp limits "delete for everyone" to roughly two days. Late detections (e.g. a domain blocklisted later) can only delete recent messages. Retro-cleanup jobs respect this limit.
7. **Handlers block the socket.** Receive → durably enqueue → ack. All analysis happens off the receive loop.
8. **Two processes, one session = `StreamReplaced`.** Each bot number must be owned by exactly one process (a Postgres lease, §15).

---

## 4. Architecture overview

One Go codebase, one binary, with **roles** you can run together (the default on the single VM) or split later.

```
                 ┌──────────────────────────── bot binary ─────────────────────────────┐
WhatsApp  ◄────► │ Connector (1 per bot number)                                        │
servers          │   whatsmeow.Client ── event handler ── Inbox writer (Postgres)       │
                 │        ▲                                   │                         │
                 │        │ paced writes                      ▼                         │
                 │   Action Executor ◄── action_queue ◄── Decision Engine ◄── Analyzer   │
                 │   (rate limit, jitter,   (Postgres,      (scores, routing,  pipeline  │
                 │    backoff, breaker)      SKIP LOCKED)    policy, trust)    T0→T4     │
                 │                                                ▲              │      │
                 │   Enrichment service (profiles, link resolve, newsletters) ◄───┘      │
                 │   Media service (thumb hash, download, pHash, OCR)                    │
                 │   AI gateway (any provider, budgets, cache) ── uncertain band only   │
                 │                                                                      │
                 │   Web/API server (dashboard, auth, settings, review, appeals)        │
                 └───────────────────────────────┬──────────────────────────────────────┘
                                                 │
                     PostgreSQL (whatsmeow store + app data + queues)
                     in-process caches (otter/ristretto); Redis/Valkey only when multi-node
                     Prometheus /metrics → Grafana; zerolog JSON logs
```

### Components

| Component | Responsibility |
|---|---|
| **Connector** | Owns a `whatsmeow.Client` for one bot number. Turns events into `inbox` rows (message events) or state updates (group/participant/picture events). Holds the session lease. |
| **Inbox** | A durable buffer of raw events (protobuf bytes + `MessageInfo` JSON). Lets us ack fast, survive crashes, and **replay** events for testing. |
| **Normalizer** | Recursively unwraps the message and builds the **Content Envelope**: every text surface, URL, media reference, contact, channel and invite (§5). |
| **Analyzer pipeline** | Tiers T0–T4 produce **signals** (§7). Tier N+1 runs only if the result is still ambiguous. |
| **Enrichment** | Cached lookups: sender profile (picture, about, business, verified name), invite-link targets, channel metadata, URL unshortening and reputation. |
| **Media service** | Thumbnail perceptual hashes, full downloads when needed, sticker frame extraction, optional OCR. CPU-bound work runs in a bounded worker pool. |
| **AI gateway** | The provider-neutral `AIClassifier` (§7.4): adapters for Anthropic, any OpenAI-compatible API (incl. local Ollama/llama.cpp/vLLM/LM Studio) and Google; context bundle; per-group and instance budgets; cluster reuse and result cache; timeouts; structured output with validation; redaction. |
| **Decision engine** | Combines signals, trust and group policy into a verdict and an **action plan** using the routing in §8.3: delete, ban, ask admins, policy delete, or nothing (warn/mute only with the optional ladder). Writes `detections` and enqueues actions. |
| **Action executor** | The *only* code that performs WhatsApp writes. Serial per bot number, with priorities, pacing, backoff, a circuit breaker and coalescing (§10). |
| **Web/API** | Dashboard, OTP login, settings, review queue, appeals, audit log, metrics. |
| **Jobs (River)** | Background and periodic jobs (§4.1): group info refresh, cache warm-up, retro-cleanup, data retention purge, list updates (URLhaus etc.), join-request polling. |

### Why this shape

- **Postgres as the only required dependency.** It covers queues (River for background jobs, `FOR UPDATE SKIP LOCKED` for WhatsApp actions), settings change notifications (`LISTEN/NOTIFY`), the whatsmeow store and app data. Coolify runs it with one click. Details and alternatives in §4.1.
- **Single binary** keeps deployment on the 2 vCPU VM trivial and makes ARM64 builds easy (pure Go where possible, §19).
- The **replayable inbox** gives us offline evaluation and regression testing on real traffic (§18).

### 4.1 Technical architecture and stack

#### Language: Go

- **whatsmeow is Go-only**, and it's the most actively maintained WhatsApp multi-device library.
- **One static binary:** a ~25 MB distroless image, `CGO_ENABLED=0` for the core, and easy **multi-arch builds (ARM64 + AMD64)**.
- **Low memory**, and **goroutines** for the many concurrent, mostly I/O-bound tasks (one connector per number, analysis workers, enrichment).
- Go's `regexp` is **RE2**: linear time, so admin-supplied patterns can't cause catastrophic backtracking.
- **Python is used only for the private, offline dataset preparation** (§7.8), which isn't part of the bot or the repository.

#### Architecture: a modular monolith

One Go codebase with **strict internal modules behind interfaces** (connector, ingestion, analyzers, decision engine, action executor, AI gateway, learning, web API, jobs). It runs as one binary in **roles** (`connector`, `workers`, `api`), all together by default or split across processes later (§15).

**Why not microservices:**
- **The bottleneck is WhatsApp's per-number write pace** (§10.3), not compute. More services don't make the bot faster.
- **The connector can't be split.** Each number must be owned by exactly one process (§3, §15).
- A message broker plus many containers is a real **burden for self-hosters**.
- Network hops between services add **latency** to the fast lane.
- **Dual writes** to a database and a broker invite consistency bugs. With Postgres as the queue, enqueuing an action is part of the same transaction as the detection that caused it.

**Natural external pieces:** the **AI provider** (a hosted API or a local server such as Ollama, §7.4) and **media tools** (`ffmpeg`/`tesseract`, run as subprocesses in the optional `-media` image).

#### Database: PostgreSQL only

One Postgres holds the **whatsmeow store**, **app data** and **queues**. There's **no SQLite "lite mode"**: one engine to support, test and migrate.

| Option | Verdict | Why |
|---|---|---|
| **PostgreSQL** | **chosen** | whatsmeow supports it; transactional queues (`SKIP LOCKED`, `LISTEN/NOTIFY`); JSONB for settings and signals; arrays + GIN for fingerprints; partitioning for growth; one-click on Coolify and every host |
| SQLite | rejected | single writer; weak for concurrent workers and queue polling; a second engine to test; no `LISTEN/NOTIFY` |
| MongoDB | rejected | whatsmeow has no Mongo store; no transactional job queue with the app data; it adds a second database anyway |
| Redis / Valkey | not needed | the queues and cache are covered by Postgres + an in-process cache. It's only an option later for multi-node rate-limit buckets (§15). |
| A separate vector DB | not needed | there are no embeddings in the core (§7.4); a future `Embedder` wouldn't justify a new service |

#### Queues and jobs

- **Background jobs** use **[River](https://github.com/riverqueue/river)**, a Postgres-backed Go job queue with **transactional enqueue**, retries with backoff, priorities, scheduled/periodic jobs and unique jobs. That covers enrichment, retention purges, nightly jobs, imports, group-info refresh, list updates and join-request polling.
- The **WhatsApp action executor stays custom** (§10). Strict **per-number serial pacing** with jitter, the fixed ceiling and slow-down is the core ban protection, and River's concurrency-limiting features are in its paid Pro tier. It's still Postgres-backed (`actions` table, `SKIP LOCKED`).
- **No external broker** (Kafka, RabbitMQ, NATS).

#### Libraries

| Concern | Choice |
|---|---|
| WhatsApp | `go.mau.fi/whatsmeow` (+ its `sqlstore` on Postgres) |
| DB access | `jackc/pgx` v5 + **sqlc** (typed queries from SQL) |
| Migrations | **goose**, migrations embedded in the binary and run on start |
| Jobs | **River** (background jobs); custom executor for WhatsApp actions |
| Cache | **otter** (in-process, TTL + size bounds) |
| Logs / metrics | **zerolog** (as whatsmeow uses) + **Prometheus** client |
| Text | Aho-Corasick (keyword lists), Go `regexp` (RE2), `golang.org/x/text` (NFKC, normalization), embedded TR39 confusables |
| Phone numbers / domains | `nyaruka/phonenumbers`, `golang.org/x/net/publicsuffix` |
| Images | `corona10/goimagehash` (pHash/dHash), `golang.org/x/image/webp` (static WebP) |
| AI | thin adapters per provider (Anthropic, OpenAI-compatible, Google) behind `AIClassifier` (§7.4) |
| Tests | standard `testing` + fuzzing, **testcontainers-go** (real Postgres in tests), a **fake `WAClient`** so most tests need no WhatsApp account |

#### Deployment shape and resources

- **Docker Compose:** `bot` + `postgres`, plus an optional **local AI server** (e.g. Ollama) profile and the optional `-media` and monitoring profiles (§19).
- **Resource estimate on the 2-vCPU / 11 GB ARM VM:**
  - the bot: ~100–300 MB RAM;
  - Postgres: ~0.5–1 GB;
  - the CPU is mostly idle between messages.
- **AI tier hardware:** a **local chat LLM on 2 ARM cores is slow** (seconds to tens of seconds per call). It's workable only because AI calls are rare and queued. For the AI tier, a **hosted provider or bigger hardware** (more cores or a GPU) is recommended.

---

## 5. Ingestion and the Content Envelope

### 5.1 Event flow

```
whatsmeow event ─► handler (returns in <1 ms; SynchronousAck after DB insert)
   ├─ *events.Message / UndecryptableMessage ─► INSERT inbox (dedupe on chat+id+edit) ─► notify worker
   ├─ *events.GroupInfo / JoinedGroup         ─► update group & member state, raid detector, join screening
   ├─ *events.Picture / UserAbout / PushName  ─► invalidate profile cache, optional re-scan of member
   ├─ *events.TemporaryBan / LoggedOut / StreamReplaced ─► trip the breaker, alert operator
   └─ Connected / Disconnected / KeepAlive*   ─► health metrics
```

Only **group** chats the bot is configured for are moderated. Two other sources feed the pipeline without being moderated:

- **`status@broadcast`**: statuses from members who share theirs with the bot. These are analyzed for the author's *person reputation* only, with no actions.
- **DMs to the bot**: routed to the **command/auth/review handler** (login codes, appeals, review decisions, admin commands, help). Spam received in DM (including broadcast-list spam) only updates the sender's reputation.

Messages from the bot itself, from group admins, and from allow-listed members skip moderation by default. Admin messages are still parsed for **commands** (§13.7).

### 5.2 Recursive unwrapping

After `evt.UnwrapRaw()`, our unwrapper loops over any remaining `FutureProofMessage` wrappers (`GroupMentionedMessage`, `SpoilerMessage`, `AssociatedChildMessage`, `StatusMentionMessage`, `GroupStatusMentionMessage`, `GroupStatusMessage`, `GroupStatusMessageV2`, `PollCreationMessageV4`, `QuestionMessage`, …) generically, by reflecting over `waE2E.Message` fields whose type is `*FutureProofMessage`. It also handles `ProtocolMessage.EditedMessage`. Depth is capped (e.g. 5) to resist crafted nesting. It records which wrappers it removed (e.g. `wrappers: ["GroupStatusMessageV2"]`) so analyzers and policy can tell a Group Status post or status mention from a plain message.

### 5.3 The Content Envelope

Every message becomes one normalized structure. Analyzers work on the envelope, never on raw protobufs. That's what makes "every content type" tractable.

```go
type Envelope struct {
    Bot, Group      JID
    Sender          Identity          // LID, PN (if known), push name, verified name
    MsgID           string            // original ID (edits point here)
    Kind            Kind              // text, image, video, sticker, contact, poll, ...
    IsEdit, IsViewOnce, IsForwarded bool
    ForwardingScore int               // ContextInfo.ForwardingScore ("forwarded many times" ≥ 5)

    Texts   []TextSurface   // {Source: "body"|"caption"|"link_title"|"vcard_biz_desc"|"poll_option"|..., Raw, Norm}
    URLs    []URLRef        // extracted + preview URL + vcard URL + location URL + event join link
    Media   []MediaRef      // {Type, SHA256, Mimetype, Size, Thumb []byte, Downloadable}
    Contacts []ContactRef   // parsed vCards: FN, ORG, TEL waid, X-WA-BIZ-NAME, X-WA-BIZ-DESCRIPTION, URL
    Channels []ChannelRef   // newsletter JIDs/names/invite codes (forwarded-from or invite)
    Invites  []InviteRef    // group invite codes / JIDs
    Mentions []JID          // incl. GroupMentions / mass-mention detection
    Ads      *AdRef         // ContextInfo.ExternalAdReply (title/body/sourceURL) — "click-to-WhatsApp" ads
    Quoted   *QuotedRef     // reply target (used for reply-spam patterns and .spam commands); IsStatus when RemoteJID == status@broadcast
    Status   *StatusRef     // ForwardOrigin=STATUS, StatusSourceType, StatusAttributionType, attributions
                            // (ActionURL, ExternalShare URLs, reshared ChannelJID, GroupStatus AuthorJID),
                            // IsGroupStatus, status-mention / group-status wrappers seen
    Wrappers []string       // FutureProof wrappers removed by the unwrapper
}
```

The text normalizer (§7.2) fills in `Texts[].Norm`. A **canonical fingerprint** is computed per envelope: SimHash of the normalized text, the media SHA256s and thumbnail pHashes, plus the target IDs of links, channels and invites.

---

## 6. Analyzers for every content type

Every message kind maps to **extractors** (fill in the envelope) and **analyzers** (emit signals). Analyzers are small, independent units behind one interface:

```go
type Analyzer interface {
    Name() string
    Tier() Tier                                   // T0..T4
    Applies(e *Envelope, p *Policy) bool
    Analyze(ctx context.Context, e *Envelope, s *State) ([]Signal, error)
}
type Signal struct {
    Code     string    // "url.blocklisted", "text.fuzzy_match", "profile.pic_known_spam"
    Weight   float64   // log-odds contribution (+ spammy, - hammy)
    Hard     bool      // policy violation, not a probability (e.g. "videos not allowed")
    Evidence string    // human-readable, shown to admins
}
```

| # | Content | What we extract | Analyzers (tiers) |
|---|---|---|---|
| 6.1 | **Text** (`Conversation`, `ExtendedText`) | body, link preview title/description/URL/thumbnail, mentions | normalization → keyword/regex/fuzzy lists (T0), URL analyzer (T1), near-duplicate & campaign (T1), language/script anomalies (T1), LLM (T3) |
| 6.2 | **Images** | caption, SHA256, thumbnail, dimensions | exact hash (T0), thumbnail pHash/dHash vs. spam set (T1), full download + pHash (T2), OCR → text pipeline (T2), vision LLM (T4) |
| 6.3 | **Video / PTV / GIF** | caption, SHA256, thumbnail, duration, size | exact hash, thumbnail pHash (T1); optional keyframe extraction with ffmpeg (T2); vision on keyframes (T4) |
| 6.4 | **Stickers / sticker packs** | SHA256, PNG thumbnail, flags (animated, lottie, AI, avatar), accessibility label, emojis; pack name, publisher, description | exact hash (T0, free and very effective since spam stickers get reused); thumbnail pHash (T1); pack text → text pipeline; first-frame pHash + OCR for stickers with text (T2) |
| 6.5 | **Documents** | file name, title, mimetype, size, page count, caption, embedded vCard | dangerous extensions (`.apk`, `.exe`, `.scr`, `.js`, double extensions), APK-in-zip (T0); filename/title text; hash reputation; optional PDF first-page render + OCR/links (T2) |
| 6.6 | **Audio / voice** | SHA256, duration, PTT flag | exact hash (repeated voice spam), policy blocks. *Transcription is optional (T4, Phase 5).* |
| 6.7 | **Contact cards & business contacts** | display name, every vCard field, `waid` numbers, biz name/description, URLs | text pipeline on **all** fields; the `waid` number goes to the **number reputation** list; enrichment of the referenced number (verified name, business profile description, profile picture hash); "contact card from a brand-new member" heuristic |
| 6.8 | **Channel shares** (forwarded-from-channel, channel invites, channel links) | newsletter JID, name, invite code, caption | channel ID list (T0); channel name text (T0); enrichment via `GetNewsletterInfo*`: description text, subscriber count, verification (T2); per-group "block channel promotion" policy |
| 6.9 | **Group invites** (invite messages, `chat.whatsapp.com` links) | invite code / group JID | blocked by default unless allow-listed (policy); enrichment via `GetGroupInfoFromLink`: group name/topic text and size, cached (T2) |
| 6.10 | **Polls** | question + options | text pipeline on every option; "poll as an ad" heuristics (options containing URLs/phone numbers) |
| 6.11 | **Locations / live locations / events** | name, address, URL, comment; event name, description, join link | text + URL pipeline |
| 6.12 | **Commerce & interactive** (product, order, payment request, buttons, list, template, ad-reply context) | all titles, bodies, URLs, prices | text + URL pipeline; payment-request messages from non-admins are a strong signal |
| 6.13 | **Unknown / future types** | every string field, via `protoreflect` | generic text + URL pipeline, plus a metric for unknown types so we notice new vectors |
| 6.14 | **Edits** | the new content, linked to the original ID | full re-analysis; "benign → spam edit" is an extra signal; deletion uses the original ID |
| 6.15 | **View-once / undecryptable** | sender, ID, timestamp | no content; policy-only (block view-once from untrusted), plus sender reputation |
| 6.16 | **The sender's profile** (not a message type, but a surface) | push name, about text, picture, verified business name, business profile (description/website/email/category) | name/about/description → text pipeline; picture → pHash vs. known spam avatars + optional vision; business category heuristics. Runs **on join** (pre-screening) and on the **first message**, then cached and refreshed when `events.Picture`/`UserAbout` fire. |
| 6.17 | **Behaviour** (across messages) | per-member and per-group rates | flood (N msgs / T), duplicate across groups (same fingerprint from the same sender in ≥ k groups), join-then-post-within-seconds, mass mentions, forward chains (`ForwardingScore`), post-then-self-delete, reaction floods |
| 6.18 | **Status content in the group** (status forwarded/shared in, replies quoting a status, status mentions of the group, Group Status posts) | the status content itself (unwrapped), quoted status text/thumbnail, `ForwardOrigin`, `StatusSourceType`, attribution `ActionURL`s, re-shared `ChannelJID`, Group Status author | full content pipeline on the status content **and** on any quoted status; attribution URLs go to the URL analyzer; reshared channel IDs go to the channel analyzer; `RESHARED_FROM_POST_MANY_TIMES` is a mild virality/spam signal; a reply whose only payload is a quoted spammy status counts as spam. Policy toggles: `status_shares`, `status_mentions`, `group_status_posts` (allow / block / block_for_new). |
| 6.19 | **The sender's own Status** (profile signal) | `status@broadcast` messages from members, when WhatsApp delivers them | content pipeline; matches feed the author's **person reputation** (a member whose status advertises a scam gets a lower trust score and stricter thresholds in every group). Best effort only: most members don't share their status with the bot's number (§2.1). |
| 6.20 | **Broadcast content** | in DMs to the bot: list JID, `BroadcastListOwner`, content. In groups: only forward metadata. | DM broadcast spam to the bot goes through the content pipeline and lowers the sender's person reputation. In groups, a forwarded broadcast is treated as a normal forward (`IsForwarded`, `ForwardingScore`). There's no way to know it came from a broadcast list or see the list name. |

### URL analyzer (used by 6.1, 6.5, 6.7, 6.8, 6.11, 6.12)

1. Extract URLs with a tolerant extractor (handles `hxxp`, `example[.]com`, `example . com`, punycode/IDN, and URLs hidden in vCards/previews).
2. Canonicalize: lowercase host, punycode ↔ Unicode, strip tracking params, compute the eTLD+1 with the Public Suffix List (`golang.org/x/net/publicsuffix`).
3. **WhatsApp-native targets** go to their own resolvers (`chat.whatsapp.com`, `whatsapp.com/channel`, `wa.me/<num>`, `wa.me/message`, `wa.me/qr`).
4. Per-group allow/deny domain lists, then the global allow-list (e.g. youtube.com, gov sites) and global deny-list.
5. Reputation: local reputation table; feeds (URLhaus, OpenPhish, the Google Safe Browsing / Web Risk API if a key is configured); domain age via RDAP (very new domains are suspicious).
6. **Shortener expansion** (bit.ly, t.co, tinyurl…): `HEAD`/`GET` with redirects capped (5), SSRF-safe dialer (no private IPs), 3 s timeout, small body limit. Results are cached.
7. Look-alike detection: confusable skeleton of the host vs. protected brands (whatsapp, paypal, banks in the user's region), and edit distance to allow-listed domains.

---

## 7. Tiered detection pipeline and scoring

### 7.0 The core decision flow: certainty routing

Every suspicious message ends up in one of three bands. This is the backbone of detection, and everything else in §7–§9 plugs into it.

| Band | What puts a message here | What happens |
|---|---|---|
| **CERTAIN** | **Exact or near-exact known spam:** media/sticker SHA256; pHash within a tight distance; SimHash within a **tight** Hamming distance (default ≤ 3 of 64 bits), or MinHash Jaccard ≥ 0.9, to a confirmed-spam entry. **Blocklisted** URL, domain, advertised number, channel, invite or media hash. The **manual blacklist** (§9.8). **Hard policy violations** (the group's content rules, §9.3). | Act at once. Spam (confidence source `known`) follows the **auto-ban matrix** (§9.2). Rule-breaking follows the group's policy action, by default a silent delete. |
| **UNCERTAIN** | Anything suspicious (score ≥ `T_review`) that isn't certain: SimHash/MinHash closeness below the tight thresholds, heuristics, unknown or new links and domains, enrichment results, behaviour signals | **The AI verifies** (T3, §7.4). It gets the message plus a few **related confirmed-spam examples** (few-shot, chosen by shared fingerprints, keywords, campaign or category; §7.4), and returns spam / not spam / unsure with a confidence and a reason. **Spam with confidence ≥ 0.9:** the message is deleted, and under the early-AI safety rule the **ban goes to admins** until the AI has a track record (§9.2). **Not spam with confidence ≥ 0.9:** nothing happens; it's logged. |
| **STILL UNCERTAIN** | The AI says unsure, or its confidence is below 0.9, or AI isn't configured or is out of budget | **Ask admins** (§9.5). The message stays visible by default (`uncertain.delete_while_pending: false`). Admins decide delete and ban. |

**Why "certain" is tight.** The bot keeps **no normal-message data** (§7.5, §17), so there's no ham set to calibrate against. The "certain" thresholds are therefore deliberately **tight and conservative**, and anything looser goes to the AI. They can only be loosened by an operator, based on review outcomes.

**What the AI is told about normal chat.** There are no ham examples, so the cached system prompt carries clear guidance on what normal group chat looks like and when to answer "not spam": ordinary conversation, questions, local buying and selling that the group allows, news links and personal photos, greetings and religious or seasonal messages, forwarded jokes, and admins' own announcements. It's told to prefer "unsure" over "spam" whenever the group's topic makes a message plausible.

**How accuracy is measured.** Not from a ham test set. **Precision** (how often a deletion or ban was right) comes from **admin review decisions** and **shadow mode**: every action shadow mode would have taken is reviewable, and every overturn counts against precision. **Recall** is measured on the **spam-only hold-out set** from the imported dataset (§7.8, §18).

### 7.1 Tiers

| Tier | Budget | What runs | Typical outcome |
|---|---|---|---|
| **T0: Policy & exact** | < 1 ms, no I/O beyond cache | content-type policy (blocked types), allow-lists (member/admin/domain), exact blocklists (keywords, regex, domains, channel IDs, numbers, media SHA256, sticker SHA256), known-spam fingerprints | The **CERTAIN** band: hard policy violations and known spam are decided here (~60–80% of spam in practice is repeats). |
| **T1: Cheap heuristics** | < 5 ms, cached I/O only | normalized + fuzzy text matching, **SimHash/MinHash near-duplicate matching** against the confirmed-spam corpus (after the fingerprint allow-list check), thumbnail pHash, URL canonical/reputation (cached), rate/flood/behaviour counters, cross-group campaign check | Most normal messages are cleared here with a low score. Suspicious ones go on to verification. |
| **T2: Enrichment** | 50 ms–3 s, network (rate-limited, cached) | profile fetch (picture hash, about, business profile), invite/channel resolution, URL unshortening/RDAP, full media download + pHash, OCR | Resolves mid-confidence cases. Async: a **first fast verdict is allowed** if T1 is already decisive. |
| **T3: Text AI** | 0.5–3 s, paid | LLM classification of the normalized text surfaces + context (structured output) | The verifier for the **UNCERTAIN** band (§7.0). Unsure or low-confidence answers go to admins. |
| **T4: Vision AI** | 1–5 s, paid | vision model on image/sticker/thumbnail/profile picture | Only when visual evidence is decisive and cheaper tiers are inconclusive. |

**Short-circuiting:** a CERTAIN match stops the pipeline. A score below `T_review` stops it as clean. Anything else continues through enrichment to the AI, if the group's settings and budget allow; otherwise it goes straight to admins.

**Two lanes:** a fast lane (T0–T1, synchronous per message, ~ms) and a slow lane (T2–T4, async workers). The fast lane handles the CERTAIN band (delete, and a ban where the matrix allows). The slow lane handles verification and asks admins.

### 7.2 Text normalization (core to "clever spam")

Applied to every text surface, producing `Norm` (for matching) while keeping `Raw` (for evidence):

1. Unicode **NFKC** (fullwidth → ASCII, ligatures, math alphanumerics 𝐟𝐫𝐞𝐞 → free, circled letters).
2. Strip **invisible characters**: zero-width (U+200B–U+200F, U+2060–U+2064, U+FEFF), bidi controls (U+202A–U+202E, U+2066–U+2069), Unicode **tag characters** (U+E0000–U+E007F), variation selectors, soft hyphen, combining-mark floods (Zalgo). The *count* of stripped characters is itself a signal.
3. **Confusables skeleton** (Unicode TR39 `confusables.txt`, embedded): Cyrillic "а" → Latin "a", Greek "ο" → "o", etc.
4. **Leetspeak** and symbol folding (`0→o, 1→i/l, 3→e, 4→a, 5→s, $→s, @→a`), applied in a *separate* variant so we don't damage numbers.
5. **Arabic/Persian normalization** (likely important for your users): remove tashkeel/tatweel, unify alef forms (أ إ آ → ا), ى→ي, ة→ه (match variant), Persian ک/ی → Arabic ك/ي, Arabic-Indic and Persian digits → ASCII digits.
6. Separator collapsing: `f.r.e.e`, `f r e e`, `f-r-e-e` → `free`; repeated-char squeeze (`freeeee` → `free`).
7. **Phone number extraction** across scripts and obfuscations ("zero five five…", spaced digits, Arabic digits) → E.164, so they can be compared against number lists.
8. Emoji handling: keep them in Raw; map common "spam emoji" sequences (💰🔥💸📈) to tokens for features.

Matching on `Norm`:

- **Keyword/phrase lists:** Aho-Corasick over normalized tokens (thousands of patterns in microseconds).
- **Regex rules:** RE2 (Go `regexp`, linear time, safe for admin-supplied patterns, with length and complexity limits).
- **Fuzzy:** token-set similarity / Damerau-Levenshtein for short phrases; **SimHash (64-bit) + MinHash LSH** for near-duplicate messages against known-spam fingerprints and for **campaign detection** (the same message, slightly varied, across groups or senders).

### 7.3 Scoring model

- Each signal contributes a **log-odds weight**. The score is `p = sigmoid(bias + Σ wᵢ)`, clipped to [0, 1]. Weights start hand-tuned (config file, versioned) and are later **calibrated** on labeled data (logistic regression; we just refit the weights, no heavy ML).
- **The same thresholds for everyone.** There's **no trust bonus**: a ten-year member and a newcomer posting the same spam get the same score, and the message is deleted either way. Trust and tenure don't change the score. They only decide *who decides the ban*: the bot, or the admins (§8.3). "Just joined" is an ordinary, invisible scoring signal (§8.2).
- **Spam score vs. policy.** Content-type rules ("no videos here") aren't spam signals. They're evaluated separately as **policy violations**, which by default lead to a silent delete with no ban (§9.3). If a message is both, the spam route applies.
- **Confidence source.** Each verdict records the strongest kind of evidence behind it, so the auto-ban matrix can be strict about which evidence is enough to ban (§9.2):
  - `known`: exact or near-duplicate fingerprint of known spam, or a blocklisted or learned domain, URL, advertised number, channel, invite or media/sticker hash, a campaign artifact, or a **blacklisted phone number** in a vCard, `wa.me` link or the text (§9.8);
  - `rules`: heuristics, enrichment, behaviour, or SimHash/MinHash closeness below the tight CERTAIN thresholds. These never act alone; they send the message to the AI;
  - `ai`: the AI verified it (with `rules` signals as its inputs). This is the source of every UNCERTAIN-band deletion.
- **Thresholds per group** (with presets):
  - `T_review` (default 0.60): "suspicious". Below it a message is clean. At or above it, a non-certain message goes to the AI for verification (§7.0).
  - **"Delete line" (`T_delete`):** reached only by CERTAIN evidence or by an AI spam verdict with confidence ≥ `ai.spam_confidence` (default 0.9). **Heuristics alone never reach it.** Without verification, a suspicious message goes to admins.
  - **"Ban line" (`T_ban`):** an auto-ban needs CERTAIN evidence (source `known`) by default, plus a matrix cell that allows it (§9.2) and routing that allows the bot to decide (§8.3). AI-verified (`ai`) evidence can be added to a matrix cell only after the AI has a track record.
- **Explanations:** every detection stores its signals, weights, confidence source and evidence, rendered as "Deleted because: link to a domain registered 3 days ago (+2.1), text 92% similar to a known crypto-scam message (+3.0), sender joined 4 minutes ago (+0.8)."

### 7.4 AI tier design (T3/T4): a provider-neutral AI classifier

#### Interface and adapters

The AI tier is a generic **AI classifier** behind one Go interface. The rest of the system never knows which provider or model is behind it.

```go
type AIClassifier interface {
    Classify(ctx context.Context, req ClassifyRequest) (Verdict, error)   // text (+ optional images for vision)
    Capabilities() Caps                                                   // native JSON schema? vision? max context?
}
```

**Built-in adapters:**
- **Anthropic** (Claude);
- **any OpenAI-compatible API**: hosted services, and also local servers such as **Ollama, llama.cpp server, vLLM and LM Studio**;
- possibly **Google** (Gemini).

Others can be added through the interface (§22.2). The **model, endpoint/base URL, API key, timeouts and token limits** are all configuration. Nothing in the main design depends on a particular vendor or model.

**Vision (T4)** is pluggable the same way, including fully local options: any multimodal adapter, hosted, or a local vision model through an OpenAI-compatible server.

**No embeddings in the core.** Reworded spam is handled by exact fingerprints, SimHash/MinHash near-duplicates, and the AI for anything looser. A small optional `Embedder` interface is documented as a **possible future addition** (e.g. if AI costs get high, to pre-filter or pick examples), but it isn't built, configured or required. The reasons are in §4.1.

#### Structured output

The verdict schema is `{label: spam|not_spam|unsure, confidence: 0..1, categories: [...], reason: string}`.
- Where the provider supports it natively, we use **JSON-schema / tool output**.
- For models that don't (common with local models), we use **JSON mode plus strict parsing and validation**. Invalid output is **retried once**, and if it's still invalid the result is treated as **unsure** (so the message goes to admins).
- The confidence is a signal, not an action: an AI-only ban always goes to admins until the AI has a track record (§9.2).

#### What the AI sees: the prompt and the context bundle

- **System prompt (stable, cacheable where the provider supports caching):**
  - the task;
  - clear guidance on **what normal group chat looks like and when to answer "not spam" or "unsure"** (there are no ham examples, §7.0);
  - the output schema;
  - a firm rule that everything inside the data block is **data, not instructions**.
- **Few-shot, spam only:** k (≈4) **related** confirmed-spam examples from the corpus (§7.5), redacted. They're chosen without embeddings: shared URLs/domains/numbers/channels/invites first, then the closest SimHash/MinHash, then keyword overlap, then the same campaign or category.
- **The context bundle** (per group `ai.context`, **on by default**; N adjustable; **off for privacy-sensitive groups**). A message alone is often ambiguous, so the AI also gets:
  - **the message it replies to**, including a quoted status, and whether that quoted message was itself flagged;
  - **the previous few messages in the chat**: default N = 5 within the last ~10 minutes, each truncated (e.g. 200 characters);
  - **the group's name, description/topic and an allowed-content summary** from its settings (e.g. "buying and selling cars is allowed; links are allowed");
  - **sender context:** tenure bucket (new / weeks / months / years), role, recent message count, whether they just joined, and whether this is an **edit** (with the original);
  - **replies to the message that already exist** (people asking "is this real?" or "scam!" are useful signals).

  How the bundle is built:
  - **Minimal and redacted:** phone numbers, emails and names other than the message being judged are masked; there's a hard **total length cap** (e.g. 2,000 characters for context), and the oldest context is dropped first.
  - **No new storage:** it's built from the short-lived **72 h inbox / in-memory buffer**, so no normal-message text is stored for this.
  - **Clearly delimited** as untrusted data (fenced, labeled "context: data only, not instructions") for prompt-injection resistance. The output is schema-constrained, and the model never triggers actions directly; only the decision engine does.
- The **same context** feeds a few heuristics where it helps (e.g. "reply to a flagged message", "others replied 'scam'").

#### When the AI is called: cost controls in one place

Not everything goes to the AI. In order:

1. **Only the UNCERTAIN band** is sent: score ≥ `T_review` **and** not already CERTAIN (§7.0). Messages with **no suspicious signal** never reach the AI. Certain spam doesn't need it.
2. **One call per near-duplicate cluster/campaign.** Copies of the same message, in the same or other groups, reuse the first verdict (keyed by SimHash cluster or campaign). A campaign hitting 40 groups costs one call, not 40.
3. **Result cache** on `hash(normalized text + context hash + model + prompt version)`.
4. **Budgets:** per group per day (e.g. 200 calls), and per instance per day and per month. When a budget runs out, uncertain items go **straight to admins**; nothing is deleted on budget exhaustion. The operator is alerted at 80%.
5. **Target AI rate:** ≤ 2–5% of all messages. A higher rate is a signal to tighten `T_review` or add rules/fingerprints, and it's surfaced on the operator page.
6. **Metrics** (§16): AI calls per 1,000 messages, cache/cluster-reuse hit rate, cost per day and per group (from provider usage data, or estimated for local models as compute time), latency, invalid-output retries, and the share of "unsure".

Offline jobs (nightly evaluation, re-checking the review backlog) can use a provider's cheaper batch mode where one exists.

#### Guardrails

- **Timeouts:** default 5 s for hosted providers; longer for local models (configurable). A timeout or error means **still uncertain** (ask admins); nothing is deleted on failure.
- **Cache** as above. Identical spam is classified once.
- **Fail safe:** with no AI configured at all, the UNCERTAIN band simply goes to admins (§7.0). The bot still works.

#### Choosing a provider and model

- **Hosted models** are usually more accurate, but message text (and context, if enabled) leaves the server for the provider.
- **Local models** (via Ollama, llama.cpp, vLLM, LM Studio) keep all data on the server, but **accuracy varies a lot** between models and languages, especially Arabic. On a small CPU-only VM they're slow: seconds to tens of seconds per call, which is workable only because AI calls are rare and queued.
- The **evaluation harness (§18)** is how a deployer compares providers and models **before trusting one**: recall on the spam hold-out, plus precision from a shadow-mode period. The website shows the results side by side.

**Example configuration** (one example; any adapter works the same way):

```yaml
ai:
  provider: openai_compatible        # anthropic | openai_compatible | google
  base_url: http://ollama:11434/v1   # a local server; or a hosted endpoint
  model: <model-name>
  api_key_env: AI_API_KEY            # empty for most local servers
  timeout: 20s
  structured_output: auto            # native schema if supported, else JSON mode + validation
```

### 7.5 Learning from confirmed spam

When someone is banned or kicked for spam, the bot keeps the evidence and uses it so the same campaign, even reworded, is caught faster next time and in other groups.

#### What counts as a learning event

We learn **only from confirmed spam**, never from every ban (admins remove people for many non-spam reasons):

| Source | Learns? |
|---|---|
| Admin review decision "spam" (§9.5), `.spam` reply, website "Confirm spam" | ✅ confirmed |
| Manual `.ban`/`.kick` with reason `spam` | ✅ confirmed |
| Manual ban/kick with reason `other`, or no reason | ❌ |
| Admin removes someone in the WhatsApp app itself (we see `events.GroupInfo.Leave` with `Sender` = an admin) | ❌ by default. Optionally, the bot asks in the review destination: "~Sara removed ~Ahmed. Was it spam? ✅ / ❌". |
| Automatic kick with score ≥ `learning.auto_learn_min_score` (default 0.98) **and** ≥ 2 independent signal families | ✅ but **provisional**: lower weight, never a hard block, upgraded when an admin confirms or the pattern is confirmed elsewhere |
| Review timeout with `auto_apply` | ✅ provisional |

#### What is saved (one corpus entry per confirmed message, plus one per confirmed spammer profile)

- **Text:** original (redacted, see retention) and normalized text of every text surface (body, caption, poll options, vCard fields, link titles, quoted status…).
- **Fingerprints:** SimHash (64-bit) and MinHash signature of the normalized text, plus extracted keywords.
- **Links:** full URLs, canonical URLs, eTLD+1 domains, unshortened targets, WhatsApp targets (invite codes/group JIDs, channel JIDs, `wa.me` numbers).
- **Media:** SHA256s, thumbnail pHash/dHash, sticker SHA256 and pack ID, document file name and extension.
- **Profile:** push name (normalized), about text, profile picture pHash, verified/business name, business description/website/email/category.
- **Contact/channel fields:** vCard names, `waid` numbers (stored as a keyed hash of the E.164 number so it can be matched, not read), `X-WA-BIZ-*` fields, channel names and descriptions.
- **Provenance:** group, detection, decider (admin person or "auto"), decision method, time, rule-set version.

#### How it feeds back

1. **Auto-fingerprinting (T0/T1):** confirmed URLs, domains, numbers, channel/invite IDs, media/sticker hashes and SimHashes become **group-scoped rules** with source `learned`. Domains that are on the global allow-list or widely used (top-sites list) are never auto-blocked. A weighted signal is added instead, and the operator sees a suggestion.
2. **Near-duplicate matching (T1):** new messages are compared against the confirmed-spam corpus by **SimHash** (LSH bands for fast lookup) and **MinHash** (Jaccard), after checking the **fingerprint allow-list** (overturned patterns, below). Within the tight thresholds it's CERTAIN (§7.0); a looser match is a `rules` signal that sends the message to the AI. This catches the common case (templates reused with small edits) in microseconds, without an embedding model.
3. **LLM few-shot (T3):** related confirmed-**spam** examples (by shared fingerprints, SimHash/MinHash closeness, keywords, or campaign/category) are passed to the AI (§7.4). There are no ham examples.
4. **Periodic re-weighting:** a weekly job fits per-signal weights (logistic regression) on decided reviews and confirmed/overturned detections. Each weight can change by at most ±20% per cycle. The new weights are replayed against recent review decisions and shadow-mode outcomes (§18), and only applied if the measured overturn rate doesn't rise and recall on the spam hold-out doesn't drop. Global weight changes need operator approval. Per-group offsets are only applied once a group has enough labels (e.g. ≥ 50).

#### Local first, global later (global = across this deployment)

A learned pattern is **group-local** at first. It's **promoted to global** (affecting all groups that opted into network intelligence) only when the same pattern (same domain/number/channel/hash, or SimHash/MinHash within the threshold) is confirmed in **≥ 3 unrelated groups** within 30 days. "Unrelated" means different owners/superadmins, member overlap under 20%, not in the same community, and different deciding admins. High-impact pattern types (domains, common short phrases) also need operator approval before going global.

#### Poisoning protection (malicious or careless admins)

- **Influence limits:** each admin can add at most N learned patterns per day (default 20), and each group at most M per day. Beyond that, entries queue for operator review.
- **Admin reputation:** if other admins in the same group overturn an admin's decisions, that admin's later contributions are marked provisional and need corroboration. Detected via `review_votes` and overturns.
- **Recent-traffic check (no stored normal text):** before a learned pattern is activated, it's matched **in memory** against the group's recent live traffic that the bot already holds for other reasons: the **72 h inbox** for text patterns, and the short-lived `message_index` fingerprints/URLs/domains for fingerprint patterns. A pattern that would match more than x% of recent non-spam traffic (e.g. someone marks "good morning" or a common domain as spam) is rejected, and the admin is told why. Nothing extra is stored. *Chosen because it's the simplest option that keeps this protection without a ham corpus. The alternative, admin-only additions + dry-run, is also kept as the manual path.*
- **New or young groups** (bot joined < 7 days ago) contribute provisional entries only and never count toward global promotion.
- **Provenance and rollback:** every entry records who caused it. The operator can "revert everything learned from admin X / group Y" in one action, and a burst detector alerts on unusual spikes in learned patterns.
- Learned patterns are never hard blocks across groups without promotion, and never override allow-lists.

#### Overturns remove what was learned

An admin "not spam" (review ❌, `.notspam`, website button, or an upheld appeal) **retracts** the corpus entry and the rules it created in that group immediately (removed from the in-memory indexes via NOTIFY). So the mistake isn't repeated, only a **fingerprint allow-list entry** is stored: the SimHash/hash of the overturned message, URL or media, with its scope. **No raw or normalized text of the overturned message is kept**, and its evidence snapshot text is deleted. The review record (who decided, when, which pattern) stays. For a **global** entry, overturns from ≥ 2 unrelated groups demote it to group-local and alert the operator. The member's trust is restored and their strike removed.

#### Retention and privacy

- Raw text in corpus entries is **redacted on save** (third-party phone numbers, emails and @mentions masked; the spam's own advertised contact numbers are kept only as keyed hashes) and kept for **180 days** (configurable).
- Fingerprints and hashes are kept while they keep matching, and expire after 180 days without a hit.
- Spammer profile data (name, about, picture hash) is stored only for *confirmed* spammers, and expires the same way.
- Groups that opt out of network intelligence contribute **nothing** to the global corpus. Their learning stays group-local.
- A privacy note (linked in the onboarding DM, the bot's profile text and on the website) explains what's stored, why and for how long. Group owners can delete their group's corpus at any time, and it's deleted automatically when the bot leaves a group.

### 7.6 Spammer intelligence database

A per-person record of everything relevant about **confirmed** spammers (people who meet the "confirmed spam" bar of §7.5, or are network-banned under §9.6). It's used to recognise the same people and campaigns when they come back with new accounts, wording or pictures. Data about ordinary members isn't put here. Their minimal data stays in `persons`/`members` (§14).

#### What's stored per spammer

| Area | Fields | Source |
|---|---|---|
| **Identities** | LID (always); phone number, country code and number prefix (when known; LID-only users may have none, and these are back-filled when learned); every identity linked to the same person | resolution sources in §7.7 |
| **Names** | push name and its **history** (each distinct value with first/last seen), normalized form, verified/business name | `Info.PushName`, `events.PushName`, `VerifiedName` |
| **Profile** | profile picture **perceptual hash** (and optionally a small encrypted thumbnail for review; see §17.1), picture ID history, about text, own Status when visible (§2.1) | `GetProfilePictureInfo`, `GetUserInfo`, `status@broadcast` |
| **Business** | business profile fields (description, website, email, category, address), verified name/level | `GetBusinessProfile` (+ raw IQ for description/website, §2.1) |
| **Group footprint** | every protected group they were in; joined at; **join method** (`invite` link via `JoinReason`, *added by* X via `GroupInfo.Sender`, *approved by* X, or already there when the bot arrived); who added/approved them; first-message time; **time from join to first message**; messages sent; left/kicked at and why | `events.GroupInfo`, `members`, `message_index` |
| **Content** | links to every message envelope recorded for them (`message_index`) and to the confirmed-spam corpus entries (§7.5): text, URLs/domains, media/sticker hashes, vCards, channel shares, invites | §5.3, §7.5 |
| **Timing** | timestamps of activity; derived patterns: hour-of-day histogram, inter-message intervals, burst signature (N groups in M minutes), join-to-spam delay | `message_index`, membership history |

#### Patterns learned over time

A nightly job computes patterns over confirmed spammers, always compared with the **base rate of legitimate members in the same groups** (lift), so common traits of normal members don't become "spam signals":

- **Number-prefix clusters:** country code, then the national prefix (e.g. the first 3–5 digits), and runs of near-sequential numbers (bulk-bought SIMs). A cluster becomes a signal only if its lift is high **and** it rests on many confirmed spammers.
- **Reused profile pictures:** pHash matches against pictures of confirmed spammers. A strong signal; the same stock/AI avatar on many accounts is common.
- **Reused names:** normalized push-name and business-name similarity across banned accounts ("Crypto Support 24/7", "Dr. Sara Investment").
- **Shared artifacts:** domains, advertised numbers, channels, invite codes and media used by several banned accounts.
- **Join-then-spam timing:** the distribution of join-to-first-message times for spammers vs. members. "Joined via link and posted a link within 60 s" is a real signal.
- **Inviters:** members (in groups where anyone can add) who repeatedly added accounts later confirmed as spam are flagged for admins. This is not acted on automatically.
- **Campaigns (account linking):** a graph whose nodes are accounts and artifacts (domain, advertised-number hash, channel JID, picture pHash, SimHash cluster, invite code). An edge means "this account used this artifact". Connected components above a size/evidence threshold become **campaigns**. A new account that touches a campaign artifact inherits a campaign prior, and admins see "likely part of campaign #31 (14 accounts, 6 domains)".

#### How it feeds scoring, and its limits

All of this produces signals in a separate **"identity prior"** family, with weights calibrated like everything else (§7.5):

- **Priors alone never delete, mute or kick.** At most they can make a message *eligible for review*, add weight to the invisible "just joined" signal, or trigger a join-screening review. The "identity prior" family doesn't count toward the ≥ 2 signal families needed for punishment or for network bans.
- **Combined cap:** all priors together add at most +1.0 log-odds.
- **Country code / number prefix:** at most +0.3 on its own. It can never be the only non-zero signal behind any action (enforced in the decision engine, with a property test, §18). It's per-group switchable (`intel.number_prefix_signal`), and the website explains it plainly. A prefix signal whose overturn rate rises is disabled automatically.
- **Exact identity match to an active network ban** isn't a prior. It's the enforcement path in §9.6, with its own safeguards.
- Upheld appeals and overturns remove the person from the database (§17.1), and the patterns are recomputed without them.

### 7.7 Identity: always the LID, the phone number when we can get it

Every person gets an internal `persons.id`.

- **The LID is the primary identity.** Every JID we see for them is stored in `identities`, normalized with `ToNonAD()`: LID, phone number, and any other form.
- **PN-addressed groups:** if we first see someone only by phone number, the person is created with the PN, and we immediately look up the LID (`Store.LIDs.GetLIDForPN`, or `GetUserInfo`, which returns `UserInfo.LID`). The LID becomes primary as soon as it's known.
- **A missing phone number is normal and fine.** The record works without it.

#### Where the phone number comes from (cheapest first)

| Source | Cost | Notes |
|---|---|---|
| `MessageInfo.SenderAlt` | free, on every message | In LID-addressed groups the server often includes the sender's phone number (`participant_pn`). |
| Participant-list events (`events.GroupInfo` join/leave/promote/demote, `JoinedGroup`) | free | whatsmeow's `parseParticipantList` already saves LID↔PN pairs to its LID store when the server includes them. |
| `GroupInfo.SenderPN`, `JoinedGroup.SenderPN` | free | phone number of the person who made a group change (e.g. who added someone) |
| `GroupParticipant.PhoneNumber` | free with the periodic `GetGroupInfo` refresh | present when the server shares it |
| `Store.LIDs.GetPNForLID` | local DB | everything whatsmeow has learned from any source |
| DMs to the bot (login, appeal, reviewer opt-in) | free | the DM's `Sender`/`SenderAlt` link that user's LID and phone number |
| A vCard they share **of themselves** (`ContactMessage.IsSelfContact`) | free + 1 batched IQ | The `waid` gives a phone number. We confirm it with `GetUserInfo`, and link it **only if** the returned LID equals the sender's LID. vCards of *other* people are never used to identify the sender. (To confirm in the spike how often `IsSelfContact` is set.) |
| `GetUserInfo([pn])` / `IsOnWhatsApp([pn])` | 1 IQ, batched, rate-limited (§10.3) | *Phone number → LID* only, used when we have a number (login, vCard, admin input) and need the LID. |

There's **no API that turns an arbitrary LID into a phone number on demand**. Hiding the number is the point of LIDs. We only learn it when the server reveals it in one of the ways above, and we don't try anything beyond these.

#### When the phone number is learned later (back-fill)

An `identity.resolved` event triggers:

1. Store the PN identity and derive the **country code and number prefix** (`nyaruka/phonenumbers`).
2. **Merge** if a separate PN-only person record exists: re-point memberships, strikes, membership history, message index, detections, corpus entries, spammer data and bans to the LID person. The merge is logged in `person_merges`.
3. **Back-fill** that person's history:
   - update number-prefix statistics (§7.6) and their spammer profile, if they have one;
   - re-evaluate `identity_watch` rules, so a "pending" network ban now matchable by this identifier is applied (§9.6);
   - apply any ban recorded against the other identifier.
4. Write it to the audit log.

#### Ban matching works with either identifier

Bans, allow-lists and watch rules are attached to the **person**, not to a JID string. `MatchPerson(jid)` looks the JID up in `identities` (LID or PN, either one alone), so a join event, message or join request matches whichever identifier it carries. If neither is linked yet, the watch rule waits until it is (§9.6).

#### Recycled numbers

Phone numbers get reassigned. If a phone number we know maps to a **different LID** than before, the old link is closed (`valid_to`) instead of overwritten. **Bans never follow a phone number to a new LID automatically**. Such cases are flagged for the operator, so the next owner of a spammer's old SIM isn't punished.

### 7.8 Importing a labeled dataset

AI and learning ship in the first version, so a new instance shouldn't start from an empty corpus. Any deployer can **import a labeled dataset** they already have. **Spam-only datasets are the default.** *Collecting and cleaning such a dataset is the deployer's own job and is not part of the bot.* (For the user's own instance, the sources are a WhatsApp group containing only spam, plus a CSV of messages. They're prepared privately and then imported with this feature like any other dataset.)

#### Command and format

```
antispam corpus import <file.jsonl|file.csv> --scope instance|group:<jid> [--holdout 0.2] [--dry-run]
```

One record per line (JSONL) or per row (CSV with a header). Only `text` is required:

| Field | Required | Meaning |
|---|---|---|
| `text` | yes* | message text, caption or other text surface (*may be empty if media fields are given) |
| `label` | no | `spam` (default) or `ham` (optional, see below) |
| `kind` | no | `text`, `link`, `image`, `video`, `sticker`, `contact`, `channel_share`, `invite`, `poll`, … (default: inferred from content) |
| `urls` | no | extra URLs (also extracted from `text` automatically) |
| `media_sha256`, `phash` | no | media/sticker fingerprints, if the dataset has them |
| `language` | no | e.g. `ar`, `en` (detected if missing) |
| `category` | no | e.g. `crypto`, `jobs`, `adult`, `promo` |
| `source` | no | free-form provenance note (e.g. "spam-only group export, 2026-10") |
| `cluster_id` | no | campaign/cluster identifier. Records with the same id are kept together when splitting train/hold-out. If missing, the importer clusters near-duplicates itself (SimHash/MinHash). |

The format, with examples, is documented in `docs/CORPUS_FORMAT.md`.

#### What import does

1. **Validate** each record and report rejected lines. `--dry-run` shows what would happen without writing anything.
2. **Normalize and fingerprint** (§7.2): normalized text, extracted URLs/domains, invites, channel IDs, numbers (stored as keyed hashes); SimHash and MinHash; plus any media hashes provided.
3. **Dedupe** against the existing corpus and within the file. Exact and near-duplicates are merged and counted, not stored twice.
4. **Split** a hold-out slice for evaluation (default 20%, stratified by category and kind) **by cluster/campaign, not by message**. Near-duplicates of a test record can't leak into the corpus, which would inflate scores. The hold-out is spam-only, so it measures **recall** (§18).
5. **Store** the rest as corpus entries with `source = import` in the **scope the operator picks**: instance-wide, or one group.
   - Imported spam produces fingerprints and SimHash/MinHash entries like confirmed spam (§7.5). Exact and tight near-duplicate matches count as `known` evidence (§7.3). Looser closeness is a `rules` signal that sends the message to the AI, so it never bans on its own.
   - **Optional `ham` records are never stored as text.** They're only used in memory during the import, to report how many would have been caught (a rough false-positive check), and, if the operator chooses, to add **fingerprint allow-list entries**.
6. **Report:** records imported, duplicates merged, clusters, the hold-out size, any categories or languages with too few examples to be useful, and, if ham records were given, the in-memory false-positive check.

Imports can be **undone** (`antispam corpus remove --import <id>`): every entry keeps its import-batch ID.

#### Spam-only by design

The corpus holds **confirmed spam only**. That's enough for fingerprints and near-duplicate matching (the CERTAIN band) and few-shot examples. What the corpus can't do without ham, the design covers elsewhere:
- **normal-chat guidance** for the AI lives in its system prompt (§7.0);
- **false positives** are measured from admin reviews and shadow mode, not from a ham set (§18);
- **poisoning protection** uses in-memory checks against recent traffic (§7.5).

#### Privacy and limits

- Import runs locally on the deployer's instance. Stored text is **redacted** like any corpus entry (third-party phone numbers and emails masked, §7.5).
- Source files are never part of the repository. Example datasets in the repo are synthetic.
- An imported dataset only bootstraps what it contains. A text-only dataset bootstraps text detection; media, sticker, profile, timing and behaviour learning start from live data unless the dataset includes those fields.

---

## 8. Avoiding false positives

False positives are the real product risk: a bot that bans a real member once loses the group's trust. The approach is: **delete spam the same way for everyone, and be careful about who gets to decide a ban.**

### 8.1 Same detection for everyone; trust only routes the ban decision

Trust is computed per *(group, member)*, plus a per-person reputation across all groups of the deployment:

| Factor | Effect |
|---|---|
| Tenure in group (from the join event, or "first seen" for pre-existing members) | grows from 0 at join to full at ~7 days (configurable) |
| Clean messages sent | each clean message adds trust (log-scaled; a cap prevents farming) |
| Admin actions | admin "vouch" or allow-list = full trust; admin `.notspam` on their message = trust boost |
| Prior strikes / deletions | reduce trust; decays over time (half-life ~30 days) |
| Cross-group reputation | clean history in other protected groups = bonus; spam elsewhere = penalty (opt-in "network intelligence") |
| Account signals | has a profile picture/about, verified business, an obviously generated push name, account device count (from `GetUserInfo`) |
| Pre-existing at onboarding | members already in the group when the bot joined start as **established** (we don't punish history) |

**Trust never changes detection.** There is no trust bonus in the score (§7.3), and spam is deleted immediately whoever sent it. Trust only decides **who makes the ban decision**:

- **Established / trusted member** (default: ≥ 7 days in the group and ≥ 20 clean messages, or pre-existing, or vouched; configurable): the bot **never auto-bans** them. It deletes the spam and asks the admins (§9.5).
- **New or unknown sender:** the bot may auto-ban, but only if the auto-ban matrix allows it for that content type and evidence (§9.2).

### 8.2 New members: an invisible "just joined" signal

- **On by default:** a sender who joined within the last 24 h gets a "just joined" scoring signal (default +0.8 log-odds, decaying to 0 over the window). It's configurable (`new_members.just_joined`: on/off, weight, window). Members never see it. There are no visible probation rules, and nobody is told they're "on probation".
- **Visible content restrictions for new members are opt-in** (e.g. `content.links: block_for_new`). When a group turns them on, they're policy rules (§9.3): silent delete, no ban.
- **Join screening** (a profile scan on join) stays on and only feeds the score. **Join-approval autopilot** (auto-approve clean requesters, auto-reject obvious spam profiles, leave the rest for humans) is opt-in.

### 8.3 Who decides the ban (routing)

The decision engine takes the score, confidence source, sender standing and group settings, and picks one route:

| Situation (bands from §7.0) | The message | The ban |
|---|---|---|
| Not suspicious (score < `T_review`) | untouched | none |
| **CERTAIN** spam, sender **new/unknown**, matrix cell = **auto-ban** for this type | **deleted at once** | **auto-ban**: kick + rejoin block, plus network rules if confirmed (§9.6) |
| **CERTAIN** spam, matrix cell = **ask admins** | deleted at once | admins decide via the review destination |
| **UNCERTAIN → the AI says spam** (confidence ≥ 0.9) | deleted | **admins decide** (early-AI safety, §9.2); after a track record, per the matrix if the cell allows `ai` |
| **STILL UNCERTAIN** (AI unsure / low confidence / AI off) | untouched by default (`uncertain.delete_while_pending`) | admins decide delete and ban |
| UNCERTAIN → AI says **not spam** (confidence ≥ 0.9) | untouched | none (logged) |
| Spam, matrix cell = **delete only** | deleted | none |
| Spam, matrix cell = **allow** | untouched | none (recorded for stats) |
| Spam (certain or verified), sender **established/trusted** | deleted | admins decide, **never auto-ban** |
| Spam, sender **possibly hacked** (§8.7) | deleted | admins decide; optional restriction while pending |
| **Policy violation only** (CERTAIN, §9.3) | deleted silently (default) | none (default) |
| Sender is an **admin** or **allow-listed** | untouched | none (a possibly hacked admin triggers an alert only, §8.7) |

The per-group safety rails (§8.6) apply on top of every route.

#### Always-ask-admins mode

A per-group mode (`enforcement.mode`) for groups that want a human behind every ban. **In this mode the bot never bans on its own.** That includes known spam and network bans coming from other groups, which also become review items. Rejoin blocks for people *this group's* admins banned still apply, because those are the admins' own decisions.

- **(a) `ask_delete_first`, recommended:** certain or AI-verified spam is deleted at once, and the **ban** goes to the admins. Members are protected immediately, and nobody is banned without a human.
- **(b) `ask_before_action`:** nothing is deleted or banned until an admin decides. The message **stays visible** meanwhile. A timeout fallback (`review.on_timeout`, default **15 minutes → delete, no ban**; or `leave`) stops spam staying up for hours if nobody answers. The fallback can never ban.

The default mode is `auto` (the routing table above). The "Ask admins" preset (§12.2) sets mode (a).

### 8.4 Shadow mode and dry runs

- **Shadow mode** (per group, and as the default for the first N hours after onboarding if the admin chooses): run everything and record what *would* have happened, but act on nothing. The dashboard shows a "this week we would have deleted 42 messages and banned 3 people; review them" summary so admins gain trust before enabling actions.
- **Rule dry-run:** a new blocklist entry or regex is tested **in memory** against the group's recent traffic before it's saved ("this would match 3 messages"): text patterns against the 72 h inbox, fingerprint/domain patterns against the short-lived `message_index`. Nothing extra is stored.

### 8.5 Review queue and appeals

- **Review:** ban decisions routed to admins (§8.3), suspicious-but-not-deleted messages, and appeals go to the admins **inside WhatsApp**: by DM to chosen reviewers or in a review group, where they decide with a reaction or a one-word reply. The website keeps the same queue and the full history. This is a first-class feature, described in [§9.5](#95-admin-reviews-and-notifications).
- **Appeals without in-group notices.** The bot is silent in groups (§9.7), so there's no kick notice with an appeal link. Instead:
  - The bot's WhatsApp **profile "about" text** (set at pairing) says: *"Group protection assistant. Removed by mistake? Message me 'appeal'."* A removed member sees a "removed by …" system line naming the bot, and can open its profile.
  - **Any inbound DM** from a person banned anywhere in the deployment gets the appeal instructions automatically.
  - A public **appeal page** on the website (`/appeal`) uses the same inbound `wa.me` flow.
  - Optional per group, **off by default**: DM the removed person once with an appeal link (`appeals.notify_removed`). It's off because an unsolicited DM to a stranger is a ban risk (§3).

  An appeal is routed like any review (§9.5). Approval lifts the ban, clears strikes, sets a temporary allow-list, and gives the member an invite link, sent as a reply in their **existing** DM thread, so it's never unsolicited.
- **Feedback loop:** every confirm/overturn is a labeled example that feeds the learning system ([§7.5](#75-learning-from-confirmed-spam)): fingerprints and near-duplicate entries, LLM examples and rule weights. Rules with an overturn rate above x% are auto-disabled and the operator is alerted.

### 8.6 Global safety rails

- Never act on admins, the bot itself, or allow-listed members.
- Per-group "max bans per hour" (default 10) and "max deletions per minute" circuit. Beyond that the bot pauses and alerts. This prevents a bad rule from emptying a group. Bans over the hourly cap are never dropped silently: they go to admins as one review item (§10.4), and batching doesn't bypass the cap.
- A global kill switch and per-rule kill switches.
- Every action is audited (who/what/why/rule version/route).

### 8.7 Hacked or compromised accounts

A long-standing member whose account is taken over is the most dangerous spammer, because other members believe them. Since there's no trust grace on detection (§8.1), their spam is deleted like anyone else's. **Known spam is always deleted**, and the confidence source `known` (§7.3) needs no extra evidence. What's left is deciding what happens to the *person*.

#### The "possibly compromised" signal family

This family is triggered by a **sudden change in a trusted member's behaviour** compared with their own history (`activity_stats`, `membership_history`):

| Signal | Example |
|---|---|
| Cross-group burst | the same (or near-same) message in ≥ 3 groups within 10 minutes, by someone who has never cross-posted |
| First-ever links or contacts | links, contact cards, channel shares or invites from a member who has never sent any |
| Campaign content | content matching an active campaign or the confirmed-spam corpus |
| Unusual hours | posting far outside their usual hour-of-day histogram (needs enough history) |
| Burst after silence | many messages after a long silence (e.g. 30+ days) |
| Profile change + spam | a new name or picture just before suspicious content (`events.Picture`, push name change) |

Two or more of these signals together, or one plus a known-spam match, mark the member as **possibly compromised** for that event.

#### The response (default)

1. **Delete** the spam.
2. **Ask the admins to decide**, through the group's review destination (§9.5): *"⚠️ ~Ahmed's account may be hacked. It just posted the same link in 4 groups (deleted). React ✅ to ban, ❌ if it's not spam, or reply `clear` if it's a hacked account you want to keep."* No auto-ban, whatever the matrix says.
3. **Optional, off by default:** restrict the member while the review is pending (`compromised.restrict_while_pending`). Their **links, media and forwards** are then deleted automatically in all protected groups until an admin decides or clears them. Plain text still goes through normal detection.
4. **Never automatically:**
   - no ban;
   - no spammer profile (§7.6);
   - no learning about the *person* (the content fingerprints are still learned, so the campaign is caught elsewhere);
   - no network ban.

   These happen only if an admin decides "ban". That also records "this isn't a hack".

**Clearing:** `.clear @member`, a `clear` reply, or the website. This closes the review without a ban, lifts any restriction, and resets the "compromised" state. Timeout fallback: keep the spam deleted, no ban.

**Admins' accounts** can be hijacked too. The bot never acts on admins (§8.6), so for an admin it only **alerts** the other admins (`compromised.admins: alert_only`), and the group can opt in to deleting known spam from admins too.

---

## 9. Enforcement

### 9.1 Action types

| Action | WhatsApp operation | Default use | Reversible? |
|---|---|---|---|
| Delete message | `SendMessage(group, BuildRevoke(group, sender, id))` | all spam ≥ `T_delete`; policy violations | No (we keep evidence) |
| **Ban** = kick + rejoin block | `UpdateGroupParticipants(group, [members], Remove)` (batched, at most `max_kick_batch` per call), plus a rejoin block (§9.6 "Keeping them out") | auto-ban route or an admin's decision (§8.3) | Yes (unban + invite link), except deleted messages |
| Reject join request | `UpdateGroupRequestParticipants(..., Reject)` | banned people, obvious spam profiles (autopilot opt-in) | Yes |
| Lockdown | `SetGroupAnnounce(true)` and/or `SetGroupJoinApprovalMode(true)`, reset invite link | raids and big waves (§9.4) | Yes |
| Ask admins | review item to the group's **review destination** (DM reviewers or review group, §9.5) | ban decisions not made automatically | n/a |
| Network ban (opt-in, quorum-gated) | cleanup in every in-scope group: ban, delete recent spam-matching messages, block rejoin, reject join requests (§9.6) | confirmed spam meeting the network rule | Yes, except deleted messages |
| *Optional:* restrict | auto-delete a member's links/media/forwards | possibly hacked account while the review is pending (off by default, §8.7) | Yes |
| *Optional:* warn, mute | warn: in-group mention (only if `silent` is off) or DM (opt-in); mute: emulated by deleting their messages | only with the optional ladder (§9.3) | Yes |

### 9.2 The per-type auto-ban matrix

For **spam from new/unknown senders**, each content type has its own action. Established members, possibly hacked accounts and ask-admins mode never auto-ban, whatever the matrix says (§8.3).

| Cell value | Meaning |
|---|---|
| `auto_ban` | delete + ban automatically when the evidence is CERTAIN (`known`), or a source the cell allows |
| `ask` | delete, then the admins decide the ban |
| `delete_only` | delete, never ban |
| `allow` | spam detection doesn't act on this type (it's still recorded for stats and learning) |

**Evidence refinement (optional per cell):** `auto_ban` lists which confidence sources (§7.3) may ban. The default is `auto_ban if source = known, else ask`: only CERTAIN evidence (§7.0) bans automatically. Heuristics and looser near-duplicate matches never act alone; they go to the AI, and an AI-verified (`ai`) ban goes to admins.

**Early-AI safety: an AI "spam" verdict can't auto-ban until it has a track record.** In every preset, a ban resting on the AI's verdict (source `ai`) routes to **ask admins**. A near-duplicate match below the tight CERTAIN thresholds never bans either, because it only feeds the AI. The website greys these sources out of the "auto-ban" choice, and `.autoban … auto ai` is refused, until that source has a **track record on this instance**: at least 200 admin decisions on reviews where it was the deciding source, with ≥ 98% of them confirmed as spam over the last 30 days (both configurable), plus the measured shadow-mode precision (§18). Even then, enabling it is an explicit operator/admin choice, never automatic. Every admin decision on these reviews feeds the learning loop (§7.5) and the track record.

**Presets fill the matrix**, so non-technical admins never have to touch it:

| Content type | Relaxed | Standard (default) | Strict | Ask admins |
|---|---|---|---|---|
| Text | delete_only | auto_ban [known] / ask | auto_ban [known] / ask ¹ | ask |
| Links | ask | auto_ban [known] / ask | auto_ban [known] / ask ¹ | ask |
| Images | delete_only | auto_ban [known] / ask | auto_ban [known] / ask ¹ | ask |
| Videos | delete_only | auto_ban [known] / ask | auto_ban [known] / ask ¹ | ask |
| Voice / audio | delete_only | ask | auto_ban [known] / ask | ask |
| Stickers | delete_only | auto_ban [known] / ask | auto_ban [known] / ask ¹ | ask |
| Documents | ask | auto_ban [known] / ask | auto_ban [known] / ask ¹ | ask |
| Contacts / business cards | ask | auto_ban [known] / ask | auto_ban [known] / ask ¹ | ask |
| Channel shares | ask | auto_ban [known] / ask | auto_ban [known] / ask ¹ | ask |
| Group invites | ask | auto_ban [known] / ask | auto_ban [known] / ask ¹ | ask |
| Polls | delete_only | ask | auto_ban [known] / ask | ask |
| Locations / events | delete_only | ask | auto_ban [known] / ask | ask |
| Status shares / Group Status | delete_only | auto_ban [known] / ask | auto_ban [known] / ask ¹ | ask |
| View-once (content unseen) | delete_only | ask | ask | ask |

Read "auto_ban [known] / ask" as: auto-ban when the evidence is CERTAIN, otherwise ask. Plain `ask` or `delete_only` applies to any evidence. ¹ Once the AI has a track record on the instance, Strict is the first preset to suggest adding `ai` to these cells.

On the website the matrix is a **simple grid** (rows = content types, one dropdown per row, and an "Advanced" toggle that shows the evidence column). By command: `.autoban videos ask`, `.autoban links auto known` (or `known,ai` after the track record), `.autoban` (show the grid as text). Changing the preset resets the grid; the website shows which cells differ from the preset.

### 9.3 Spam vs. rule-breaking

Two different things, handled differently:

- **Spam** is what detection finds (§7). It follows the routing in §8.3 and the matrix in §9.2. **Auto-ban is only for detected spam.**
- **Rule-breaking** is a normal message that breaks a group's content rules, e.g. a video in a no-videos group, or a link from a new member when the group opted in to `block_for_new`. Content policy per type is `allow` / `block` / `block_for_new` (opt-in) / `block_for_untrusted` (opt-in). The default reaction to a violation (`policy.action`) is **delete only, silently**: no ban, no strike, no message. Groups can choose `ask` or `ban` for violations, and optionally `policy.repeat`, e.g. "after 3 violations in 7 days, ask admins". Both are off by default.

#### Optional ladder (off by default)

The warn → mute → ban ladder still exists for groups that want it (`ladder.enabled: false` by default):

```yaml
ladder:
  enabled: false
  window: 168h
  steps:
    - at: 1   do: [delete, warn]        # warn needs silent: false (in-group) or warn_via: dm (opt-in)
    - at: 2   do: [delete, mute: 1h]
    - at: 3   do: [delete, ban]
```

When enabled, it replaces the matrix's `auto_ban` for the first strikes of new/unknown senders. Established members still go to admins.

**Mute emulation** (only with the ladder): while muted, every new message from that member is deleted at P1, coalesced and paced like everything else. If a muted member floods, mute escalates to a ban (configurable), so we don't burn the write budget on deletes.

### 9.4 Raid / flood mode

The detector watches per-group join and message velocity:

- **Join raid** (e.g. ≥ 8 joins in 2 min with ≥ 3 suspicious profiles): enable join approval, reset the invite link, and kick or reject the suspicious joiners in **batched calls** (at most `max_kick_batch` each, within the per-group kick cap; the rest go to admins as one review item). Notify admins.
- **Message flood** (many senders posting near-duplicate content), **or** a deletion backlog that would take more than `raid.lockdown_when_backlog_seconds` (default 60 s): switch to `SetGroupAnnounce(true)` ("only admins can send") **early**. That's one write that stops thousands of messages, instead of thousands of individual deletes. Then ban the spammers in batched calls (capped per call, §10.4), clean up newest-first in the background, and reopen automatically after `raid.auto_lockdown_minutes` or when the backlog is clear, whichever is later. Admins get one alert in the review destination (nothing is posted in the group, §9.7) and can reopen early with `.unlock`.

### 9.5 Admin reviews and notifications

A first-class, per-group feature that can be configured from the website and by command. It's where the bot asks humans whenever it isn't sure, and where it reports what it did.

#### What goes to review

Configurable per group (`review.send`):

| Item | Default | Notes |
|---|---|---|
| **Ban decisions** routed to admins (§8.3): spam from established members, `ask` cells in the matrix, auto-ban conditions not met, possibly hacked accounts, everything in ask-admins mode | always on | The core use. The spam is usually already deleted; admins decide whether to ban. In ask-admins mode (b), the message is still visible. |
| Suspicious but not deleted (`T_review ≤ score < T_delete`) | on | "Have a look" items. |
| Appeals | on | §8.5 |
| Raids, "bot lost admin", breaker open | on (notify only, no decision) | Alerts. |
| Daily digest | off | One summary message per day instead of a message per item. |

While an item waits, the spam is normally already deleted and only the **ban** waits. Only ask-admins mode (b) (`enforcement.mode: ask_before_action`, §8.3) leaves the message visible until an admin decides or the short timeout hits. New groups start in mode (b) unless the operator configures otherwise (§12.2).

#### Where reviews go

Each group chooses one destination (`review.destination`):

- **(a) `dm`: chosen reviewers by DM.** The admin picks specific admins. Each chosen reviewer must **opt in once** by messaging the bot: a `wa.me/<bot>?text=REVIEWER%20<code>` link that the website shows the choosing admin, who forwards it to the reviewer; or the reviewer logs in to the website (an inbound login counts); or they send `.reviewer on` in DM. Nothing is posted in the protected group. Until then the bot doesn't DM them. That keeps every review DM part of a conversation the reviewer started, so they're never unsolicited (ban risk, §3). Reviewers who haven't opted in are shown as "pending" on the website.
- **(b) `group`: a review group.** Any **separate, non-protected** group the bot is a member of (it doesn't need to be admin there). A protected group can't be its own review group, because the bot is silent there (§9.7). An admin sets it by sending `.reviewgroup here` *inside* that group. One review group can serve several protected groups (one moderation team for many groups), and every item names its group. The bot warns if the review group contains people who aren't admins of the protected group, because they'd see the evidence.
- **(c) `dashboard`: website only.** Nothing is sent in WhatsApp.

A **fallback chain** applies if the primary destination is unusable (all reviewers removed, the bot was removed from the review group): dm → group → dashboard-only. The owner is told once.

#### The review message

Sent as text, or as an image with a caption when there's a thumbnail (the inline thumbnail is re-uploaded, never the full media; optional blur for groups that set `review.blur_media`). Example:

```
🛡️ Review #K7Q2 · Riyadh Cars Market
Who:  ~Ahmed · +966 5•• ••• •12 · member for 5 months · 312 earlier messages
What: "💰 اربح ٣٠٠٠ ريال يومياً… join my channel" + link bit.ly/… → t.me/…
Why:  link to a 3-day-old domain (+2.1) · 91% similar to confirmed scam #4411 (+3.0) · same link in 3 groups (+1.2)
Score: 0.97 · Source: rules · Message: deleted ✔ · Why asking: established member (5 months)
React ✅ ban · ❌ not spam
or reply: ban · ok · delete only
Auto-decision in 6h: keep deleted, no ban
```

The text is redacted: third-party phone numbers and emails are masked, and the text is truncated to about 300 characters. The language follows `review.language`.

#### How admins decide

- **Reactions:** a `ReactionMessage` whose `Key.ID` matches a review message we sent (`review_messages` maps our outgoing message IDs to reviews). Default mapping: ✅ 👍 = spam, ban; ❌ 👎 = not spam. 🔇 = mute only appears when the group enabled the optional ladder (§9.3). Configurable. Removing a reaction (empty `Text`) before the decision is final withdraws that vote.
- **Replies:** any message whose `ContextInfo.StanzaID` is a review message ID, parsed for keywords in English and Arabic (`ban`/`kick`/`طرد`/`حظر`, `ok`/`not spam`/`ليس سبام`, `delete only`, `clear` for hacked accounts, and `mute 2h`/`كتم` only with the ladder), so admins can pick a different action than the proposed one.
- **Other ways:** the website review queue, or `.review K7Q2 ban` in a DM with the bot or in the review group.
- **No interactive buttons.** `ButtonsMessage`/`ListMessage`/`InteractiveMessage` sent by unofficial clients often don't render or are blocked on some devices, so we depend only on reactions and replies, which always work.

#### Who may decide, and first-wins

- A vote counts only if the voter is a **current admin of the protected group** the item belongs to. That's checked against the participant cache, refreshed if older than 10 minutes. In `dm` mode the voter must also be one of the chosen reviewers, or decide through the dashboard. Members of a review group who aren't admins of that protected group are ignored, and the attempt is logged.
- **The first valid decision wins**, atomically: `UPDATE reviews SET status='decided', decided_by=…, decision=…, method=… WHERE id=$1 AND status='pending'`. Later votes get a cheap 👌 reaction from the bot plus "already handled by ~Sara" at most once per voter. All votes, including late and invalid ones, are stored in `review_votes` for the audit log.
- The decision is executed through the normal action path (ban + rejoin block, network rules in §9.6, learning in §7.5) and recorded as *who decided, how and when*.

#### Showing the outcome

So other admins see the item is handled, the bot **edits the review message** (`BuildEdit`) to append `✔ Handled by ~Sara · banned · 12:41`. whatsmeow's `EditWindow` is **20 minutes**. After that, editing isn't possible and the bot posts a short **follow-up reply** that quotes the review message. In `dm` mode, every reviewer who received the item gets the update. To bound the cost, updates to the *other* reviewers are batched into their next review message or digest when there are more than 3 of them.

#### Timeouts

Per group (`review.timeout`, default 6 h; 15 min in ask-admins mode (b)) with a fallback (`review.on_timeout`):

- `keep_deleted_no_ban` (default): the message stays deleted, with no strike and no ban.
- `delete_no_ban` (ask-admins mode (b) default): delete the still-visible message, no ban.
- `auto_ban`: ban as proposed. **Not allowed** in ask-admins mode, or for established members or hacked accounts, because a timeout must never become an automatic ban of someone the bot wasn't allowed to ban.
- `dismiss`: leave the message, restore the member's trust.

The scheduler applies the fallback, posts "⏱ No decision in 6h → kept deleted, no ban" (in the review destination only), and records `method = timeout`. **Quiet hours** (`review.quiet_hours`, e.g. 00:00–07:00 local time) delay DMs but don't stop the timeout clock; the item goes into the morning digest.

#### Pacing

All review posts, edits, follow-ups and outcome reactions are actions in the paced queue (§10) at **P3 (notification)**, with their own sub-budget so a raid can't starve deletions or vice versa. Per reviewer: at most 10 review DMs per hour. Beyond that, items are folded into a digest message ("12 more items, decide on the website or reply `all ok` / `all spam`").

### 9.6 Network bans: full cleanup across the network

When a spammer is banned, the bot cleans up after them everywhere it can and keeps them out, but only under strict rules, because a wrong network ban hurts someone in many communities at once.

#### Ban levels

| Level | When | Effect |
|---|---|---|
| **Local ban** | any ban or kick (reason `spam` or `other`) | kick from *this* group, block rejoin to *this* group only. Never leaves the group. |
| **Network candidate** | a ban with reason `spam` that doesn't yet meet the network rule | local ban, plus the person is recorded as a candidate. Other in-scope groups get a **strong risk signal** (stricter thresholds, join-screening review), but no automatic kick. |
| **Network ban (active)** | a `spam` ban that meets the network rule (below) | full cleanup and rejoin blocking in every group whose settings include it |

`other`/personal bans **never** leave the group they happened in.

#### The network rule (quorum)

A network ban activates only if **all** of these hold:

1. **Reason is spam** and the person was **confirmed** as a spammer by an admin (review ✅, `.spam`, `.ban … spam`, website). An automatic kick without any human confirmation needs operator approval to go network-wide.
2. **And at least one of:**
   - **(a) strong evidence:** the detection score is ≥ 0.97 with ≥ 2 independent signal families (e.g. content *and* behaviour/profile). Weak identity priors (§7.6) don't count as a family.
   - **(b) independent confirmation:** confirmed as spam in **≥ 2 unrelated groups** within 30 days. Unrelated means different owners/superadmins, member overlap under 20%, not in the same community, and different confirming admins (same definition as §7.5).
   - **(c) operator decision.**
3. **Protections not triggered:**
   - The person is not an admin of any protected group. That case always goes to the operator.
   - The person is not an *established* member elsewhere (for example ≥ 90 days and ≥ 50 clean messages in another protected group). Established members go to the operator or to that group's admins for review instead of being kicked automatically.
   - The banning admin isn't flagged by admin reputation (§7.5), and hasn't exceeded the per-admin network-ban limit (default 5 per day).

So a single admin of one group can't evict someone from unrelated communities on their word alone. Either the evidence is very strong, or a second unrelated community independently agrees, or the operator decides.

#### Opt-in and scope (per group)

A clear setting on the website and by command (`.network off|org|all`):

| Setting | Meaning |
|---|---|
| **Shared ban list: Off** | This group neither shares its spam bans nor enforces anyone else's. Network-banned people are still scored normally. |
| **My organisation only** (default) | Network bans are shared and enforced only between groups in the same **organisation**: groups with the same owner/superadmin, in the same WhatsApp community (`GroupLinkedParent` / `GetSubGroups`), or linked as one organisation on the website by someone who is admin in all of them. People network-banned *outside* the organisation get a strong risk signal and a join review here, but no automatic kick. |
| **All protected groups** | Bans from any opted-in group *of this deployment* that meet the network rule are enforced here. |

Contribution and enforcement can be split (`network.contribute`, `network.enforce`) for groups that want to share but not receive, or the other way round.

#### What cleanup does

1. **Resolve the person's identities** (§7.7): the LID is always known, and the phone number when it has been resolved. Each group is matched with whichever identifier it uses.
   - **Only one identifier known:** match on that one. If a group's participants use the other identifier and no mapping is known yet, the group is marked **"identity pending"**. The ban becomes a *watch rule*, and the first time the person appears there (join event, message, participant refresh), the mapping is learned and the ban is applied.
2. **Plan, per group in scope where the bot is a member:**
   - **kick** if they're currently a participant (from the participant cache, refreshed if stale);
   - **re-scan** their recent recorded messages (next step). Only the ones that match spam are deleted.
3. **Delete only the spam, not everything they sent.** Every recent recorded message from the person, in every in-scope group, goes back through the detection pipeline (T0–T2, plus T3 if the group has AI on). The pipeline now knows the ban evidence and the person's confirmed-spammer status. A message is deleted if **any** of these hold:
   - it shares a **fingerprint** with the ban evidence (SimHash/MinHash within threshold, the same URL/domain, advertised number, channel, invite or media/sticker hash);
   - it belongs to the **same campaign** (§7.6), meaning it touches an artifact of that campaign;
   - it scores **≥ that group's `T_delete`** on its own merits. The confirmed-spammer status counts only as a capped identity prior, so it can't push a normal message over the line by itself.

   Everything else, like ordinary chat, replies or a "thanks", is **left alone** and listed in the report as "kept". The result of each re-scan is stored as a detection with `mode = network_rescan`, so admins can see why each message was deleted or kept.
4. **How far back, and the limits:**
   - **What the bot keeps:** the raw message content (inbox) for **72 hours**, and message metadata and fingerprints (`message_index`) for **30 days** (§14). The re-scan needs content, so it covers the last 72 hours. That's longer than WhatsApp's **delete-for-everyone window of about 2 days** (exact limit to confirm in the spike), so everything still deletable can be re-scanned.
   - Messages 2–30 days old can still be *matched* by fingerprint and listed in the report as "spam, too old to delete", but they can't be removed.
   - Only messages the bot **recorded** can be found, i.e. messages sent while the bot was in the group and connected (or delivered on reconnect). Messages from before the bot joined can't be found. WhatsApp offers no "search a member's messages" API, and history sync on linked devices is too limited to rely on.
   - Deleting requires the bot to be an **admin at cleanup time**. Groups where it isn't are listed as skipped.
5. **Report:** the originating group's review destination gets one summary ("Network ban of ~Ahmed: kicked from 12 groups, deleted 9 spam messages, kept 14 normal messages, 4 spam messages too old to delete, 2 groups skipped: bot not admin"). Each affected group gets a line in its next digest or notification with the reason category (not the other group's message content unless the groups share an organisation) and an **undo** option.

#### Batching through the action queue

A ban touching dozens of groups must not trip rate limits:

- Actions are planned as one **network-ban job** with child actions, each routed to the bot that is primary in that group (§15).
- **Priorities:**
  - P0: delete and kick in the group where the spam is happening right now.
  - P1: deleting *spam-matching* messages sent in the last hour anywhere (they're being read now). The re-scan runs in the analysis workers, not the action queue, so it costs no WhatsApp writes.
  - P2: kicks elsewhere.
  - P4: older spam-matching deletions, still before their WhatsApp deadline.
- **Batch API use:** kicks to the same group within the coalescing window are merged into one `UpdateGroupParticipants(group, [jid1, jid2, …], Remove)` call. During a raid, ten banned accounts in one group means two calls (with the default batch cap of 8), not ten. Each account must have met the ban rules individually, and the per-group kick cap still applies (§10.4). Several identities of the same person aren't sent twice; we pick the identity the group is addressed with.
- **Pacing:** the normal per-bot and per-group buckets apply. Example: 40 groups with 1 kick + 2 spam deletions each is about 120 writes, roughly 6 minutes at the default ceiling of 20 writes/min, with priority work interleaved. Older deletions get deadlines and are dropped if they can't make the WhatsApp window.
- **Idempotent:** dedupe keys per (ban, group, action), so a restart or a second confirmation doesn't redo work.

#### Keeping them out

- **Rejoin:** on `events.GroupInfo` with `Join` containing a network-banned identity in an in-scope group, the bot kicks them at P0 (batched with other joiners in a raid). In groups using **always-ask-admins mode**, an incoming network ban is a review item instead ("~X is network-banned for spam in 3 groups. Ban here? ✅"). Rejoin blocks from the group's own bans always apply.
- **Join requests:** in approval-mode groups, matching requests are rejected via `UpdateGroupRequestParticipants(…, Reject)`. Requests are found by parsing `GroupInfo.UnknownChanges` or by polling (§2.3).
- **Admin override:** if a *current admin of that group* deliberately re-adds the person (`GroupInfo.Sender` is that admin, and `JoinReason` isn't an invite link), the bot does **not** kick them. It treats this as a local exception, allow-lists the person in that group, and tells the admins once ("~Sara re-added a network-banned user; they're allowed here. Undo: `.unallow @…`"). Admins stay in control of their own group.

#### Unban and appeals undo it network-wide

A network unban comes from:

- an upheld appeal (routed to the originating group's review destination; to the operator when several groups contributed);
- the operator;
- or, while the ban rests on a single group's confirmation, that group's admins.

It reverses everything that can be reversed:

- deactivates the ban and cancels pending child actions;
- removes rejoin blocks in all groups;
- restores trust and person reputation, and clears the strikes caused by the ban;
- retracts learned patterns (§7.5) and the spammer profile (§7.6);
- tells each affected group's admins.

The person is **not re-added automatically**. Adding someone depends on their privacy settings and would be unsolicited. Instead they get an invite link per group they were removed from, sent as replies in their own appeal DM thread. Groups can choose not to send links. Deleted messages can't be restored.

#### Audit

Every step is in `network_actions` + `audit_log`: the ban, which quorum branch it met, every confirmation, each planned/executed/skipped action per group with its error, overrides, and the unban. Operators see everything. Each group's admins see the entries that touched their group.

### 9.7 Silent by default

**Nothing is posted in protected groups.** That means no welcome or activation message, no "make me admin" message, no warnings, no in-group notices, and no command replies in the group. Admins interact with the bot by **DM**, in the **review group** (which must be a separate, non-protected group), or on the **website**.

| What used to be in the group | Where it happens now |
|---|---|
| Onboarding / activation / "make me admin" | DM with the admin who added or promoted the bot, **inbound-first** (only once they've messaged the bot), or the website (§13.6) |
| Health nudges ("bot lost admin", many unanswered reviews) | review destination, DM to opted-in admins, or a website banner |
| Warnings | none by default; only with the optional ladder and `silent: false` or `warn_via: dm` |
| Command replies | the command is acted on, **the command message is deleted**, and the reply goes by DM (§13.7) |
| Kick notices with an appeal link | the bot's profile "about" text, automatic replies to DMs from removed people, the `/appeal` page (§8.5) |
| Raid / "spam wave removed" notices | the review destination only |

**What members can still see.** Being honest about the limits:
- the bot is in the **member list, marked as admin**;
- deleted messages show as **"This message was deleted by admin"**;
- bans show WhatsApp's own system line ("… removed …");
- lockdown shows WhatsApp's system line about who can send messages.

The bot can't hide these.

**Recommendation:** give the bot number a **neutral name and picture** (e.g. "Group Assistant" with a plain icon, not "AntiSpam Bot"), and set its about text to the appeal line in §8.5. The setup guide includes this.

Groups that *want* visible messages (warnings, a lockdown explanation) can set `silent: false`. Even then, every in-group post is rate-limited (at most 1 per group per minute) and paced.

### 9.8 Manual phone-number blacklist (first version, by command)

Admins often already know a spammer's number. The blacklist lets them act on it directly.

**Commands** (admins only; in a DM, the review group, or the protected group, where the command is deleted and the reply goes by DM, §9.7):

| Command | Effect |
|---|---|
| `.blacklist add <number> [<number> …] [reason]` | add one or more numbers |
| `.blacklist remove <number> [<number> …]` | remove them (this is the undo) |
| `.blacklist list` | show this group's entries (masked except the last digits), with who added them and why |
| `.blacklist check <number>` | is it listed (here or instance-wide), is it a member, what's been done |

Numbers are accepted in any common format (`+966 55 123 4567`, `0551234567` with the group's default country, `00966…`, Arabic-Indic digits, `wa.me/9665…` links) and normalized to **E.164** with `nyaruka/phonenumbers`. Invalid numbers are reported back, not silently dropped.

**Scope:**
- **Per group** by default.
- The **operator** can add **instance-wide** entries (`.blacklist add --instance …`, or the operator page). Groups use them unless they opt out (`blacklist.use_instance: false`).
- In **always-ask-admins** groups, instance-wide entries become review items ("~X is on the instance blacklist. Ban here? ✅"). The group's own entries apply directly, because they're that group's admins' decisions.

**What happens:**
1. **Resolve the identity** (§7.7): `GetUserInfo([pn])` / the LID store returns the LID. Lookups are batched and paced in the read budget (§10.3). Both identities are stored and linked to the person. If the number isn't on WhatsApp (`IsOnWhatsApp`), the entry still works as a content signal.
2. **If they're a member now:** **ban** them (kick + rejoin block) silently through the paced queue, batched with other kicks. Blacklisting is an admin decision, so it applies to established members too. The only exception is that it **never acts on group admins**: the adder is told instead (§8.6).
3. **From then on:**
   - **auto-kick on join**;
   - **reject their join requests** in approval-mode groups;
   - **delete any message** from them on sight (P1), plus a ban if they're somehow still in the group.
4. **Content signal:** the number appearing **in a shared vCard** (`waid`/`TEL`), **in a `wa.me` link**, or as an **advertised number in message text** (§7.2 number extraction) counts as known spam (confidence source **`known`**, §7.3). In practice, posting a blacklisted contact gets the message deleted and, under the matrix, the poster auto-banned if they're a new sender.
5. **Undo:** `.blacklist remove` lifts the rejoin block and the content signal. Like any unban, it doesn't re-add the person automatically.

**Audit:** every add/remove is logged with who, when, why, the scope, and the resulting actions (kicked from which groups, messages deleted). Numbers are shown masked in group-admin views and in full to the operator. Blacklisted numbers are personal data (§17.1): entries keep their reason and are reviewed under the same retention rules.

**Website:** a blacklist page (add, remove, search, history) comes with the website in Phase 3. In Phase 1 it's commands only.

---

## 10. Outbound action queue

All WhatsApp **writes** (and expensive IQ reads) go through here. It's the main defence against account bans.

### 10.1 Structure

- **Custom, not River** (§4.1): strict per-number serial pacing is the core ban protection.
- **Persistent** table `actions` (Postgres): `id, bot, group, kind, payload, priority, status, attempts, not_before, dedupe_key, created_at, reason_detection_id`.
- **One executor goroutine per bot number** (strictly serial, one action at a time) pulls with `SELECT … FOR UPDATE SKIP LOCKED ORDER BY priority, not_before, id`.
- In memory: a priority heap holds only the next few hundred actions per priority, refilled from the table. The table is the source of truth (crash-safe).
- **No maximum queue length.** The table grows as needed. Work is never dropped because the queue is "full"; it's only dropped when it can no longer matter (its deadline passed, its message was already deleted, or a better action replaced it). Backlog size and an estimated time to drain are shown per group and per bot (§16).

### 10.2 Priorities

| Priority | Examples |
|---|---|
| **P0** | Raid response (lockdown, batch kick), delete of high-confidence scam with an active link |
| **P1** | Spam deletions, policy deletions, restriction/mute-enforcement deletions (optional features) |
| **P2** | Bans (kick + rejoin block), join-request rejections |
| **P3** | Review messages and their edits/follow-ups (§9.5), DMs (command replies, onboarding, OTP), admin notifications, optional warnings (OTPs and reviews each have a small reserved sub-budget so neither can be starved) |
| **P4** | Background: enrichment IQs (invite/channel resolution, profile fetch), retro-cleanup |

### 10.3 Pacing and rate limits

> **In plain words:** the bot never refuses work because the queue is too long; it only controls how *fast* it sends actions to WhatsApp, because sending too fast is what gets numbers banned.

- **Spacing:** each write is followed by a short pause with random jitter (base ~1 s + 0–1.5 s), so the pattern doesn't look machine-regular. Configurable per bot.
- **A fixed ceiling that the rate never climbs above on its own (slow down only):**
  - Each bot has a **ceiling**: default **20 writes/min** sustained, with a small **burst allowance of 10 writes** for the first seconds of a wave. Configurable per bot. The real default will be set from the Phase 0 spike measurements, and this starting value is deliberately conservative.
  - The effective rate **never rises above the ceiling automatically.** Success isn't evidence that WhatsApp is fine with a rate: bans and temp bans can arrive without any warning, and `429 rate-overlimit` mostly applies to IQ queries, not to message sends, revokes or participant updates.
  - **On any warning sign, slow down sharply:** a 429, unusual or unexpected errors, slow server acks (ack latency well above the bot's normal), or a disconnect shortly after writes. The rate drops to a quarter of the current value (minimum 2 writes/min) and recovers slowly, e.g. +1 write/min every 15 calm minutes, **up to the ceiling at most, never beyond it.** Repeated warning signs open the circuit breaker (§10.5).
  - **Raising the ceiling is a manual operator decision** (config or operator page), documented as risky: it increases the chance of a ban, it's logged in the audit log, and the setup guide recommends doing it only with measurements from your own number and only in small steps.
  - **Daily alert, not a target:** an operator alert fires when a bot passes **7,500 writes/day** (about a quarter of the 28,800 the ceiling allows in theory). It doesn't slow or stop anything. It just tells the operator that the bot is unusually busy, so they can check for a raid, a bad rule or a misconfigured group. Configurable.
- **Fairness between groups:** per-group buckets (default ≤ 10 writes/min per group, i.e. half the bot's ceiling) and round-robin within each priority, so one raided group can't starve the others. With a fixed ceiling, **lockdown and batching are the main way one number copes with big waves** (§9.4, §10.4). One announce-mode write protects a group better than hundreds of deletes.
- **Cheap reads, separately budgeted:** reads (IQs) have their own buckets so they never use up the write budget:
  - `GetUserInfo` is batched (up to ~50 JIDs per call);
  - profile pictures use `ExistingID`, so an unchanged picture costs nothing to re-check;
  - invite/channel lookups are cached for 24 h, with negative caching;
  - media analysis uses inline thumbnails before any download.
- **Message sends** (reviews, DMs, and optional warnings) have extra limits: no identical text twice in a row in different chats (templates vary). Nothing is posted in protected groups unless `silent` is off, and then at most 1 post per group per minute.

### 10.4 Coalescing and dedup

- **Kicks:** queued removals for the same group within a 2–3 s window merge into `UpdateGroupParticipants(group, [JIDs], Remove)` calls. The same goes for join-request rejections (`UpdateGroupRequestParticipants`).
  - **Batch-size cap:** at most `pacing.max_kick_batch` participants per call (default **8**; the spike will test 5–10 and larger sizes to see how WhatsApp reacts). Larger sets are split into several calls, each paced like any other write.
  - **Batching never lowers the bar:** every person in a batch must have met the kick rules **individually** (score, signal families, trust gating, network-ban quorum). Batching only changes how many API calls the same decisions take.
  - The per-group **`limits.max_bans_per_hour`** cap (§8.6) still applies across all batches. Bans beyond it aren't dropped: they go to the review destination as one "kick the remaining N?" item. Lockdown keeps those accounts from posting in the meantime.
- **Newest and most visible first:** within a priority, deletions in the same group are ordered **newest first**. The newest messages are what members see when they open the chat, and older spam is already scrolled away. Ordering is also **deadline-aware**: anything about to leave WhatsApp's ~2-day delete window is moved ahead of work that can wait.
- **Lockdown beats deleting one by one:** when a group's projected backlog would take more than `raid.lockdown_when_backlog_seconds` (default 60 s) to clear, the bot escalates to announce mode early (§9.4). One write stops all new spam, then the backlog is drained calmly.
- **Dedup key** (e.g. `revoke:<group>:<msgid>`) prevents double deletes from edits/retries.
- **Cancellation:** if the sender deletes the message themselves (REVOKE event), its pending revoke is dropped. If a member leaves, pending review DMs about them stay (the ban still matters for rejoin and network rules).
- **Expiry:** each action has a `deadline` (a delete older than ~48 h can't be done; an optional warning older than 10 min is pointless) and is dropped after it.

### 10.5 Errors, backoff and the circuit breaker

- **Transient errors** (timeout, disconnected, 500/503): exponential backoff `min(2^n · 2 s, 5 min)` with full jitter, max 6 attempts.
- **`ErrIQRateOverLimit` (429):** pause that bot's executor for 60 s, then apply the slow-down (rate to a quarter, slow recovery to the ceiling at most, §10.3). The same applies to the other warning signs: unusual errors, slow acks, a disconnect right after writes.
- **Permanent errors** (403 not admin, 404 message gone, participant not in group): fail fast, mark the action, update state (e.g. "bot is no longer admin in group X", which surfaces in the dashboard).
- **Circuit breaker:** on `events.TemporaryBan`, `LoggedOut` or repeated 429s, open the breaker. All writes stop except P0 lockdowns (configurable), and the operator is alerted (dashboard banner + Telegram/email/webhook). During a temp ban the bot keeps *reading* and records detections so it can clean up afterwards within the delete window.
- **Kill switch:** a global "pause all actions" toggle in the operator dashboard.

---

## 11. Caching

Two layers: **in-process** (fast, per node; `maypok86/otter` or `ristretto` with TTL + size bounds) backed by **Postgres** for anything expensive to recompute. Redis is added only when going multi-node.

| Cache | Key | TTL / invalidation | Store |
|---|---|---|---|
| Group settings / policy | group JID | until a `LISTEN/NOTIFY` change event (versioned) | memory ← Postgres |
| Group info & participants (admins!) | group JID | refreshed on `events.GroupInfo`, plus a periodic refresh every 6 h | memory + Postgres |
| Member state (trust/established, strikes, bans, restrictions) | (group, member) | write-through | memory + Postgres |
| Profile snapshot (picture ID/hash, about, verified name, business profile) | person LID | keyed on `PictureID` (`ExistingID` makes refresh calls cheap); 24 h TTL; invalidated by `events.Picture`/`UserAbout` | Postgres |
| Media hash results | media SHA256 | permanent (content-addressed) | Postgres |
| Thumbnail pHash results | pHash | permanent | Postgres |
| Verdict cache | envelope fingerprint (no text) | 7 days; "clean" verdicts are purged when the rule-set version changes | memory + Postgres |
| AI results | sha256(normalized text + model + prompt version) | 30 days | Postgres |
| Link resolutions | URL / invite code / channel code | 24 h (invites/channels), 7 d (unshortened URLs), negative cache 1 h | Postgres |
| Domain reputation / RDAP | eTLD+1 | 1–7 days; feed updates replace | Postgres |
| LID ↔ PN | — | whatsmeow's own `LIDStore` | whatsmeow store |

Fingerprint sets used in T0/T1 (blocklists, spam SimHashes, pHashes) are loaded **into memory** at start (they're small: tens of MB even at scale) and refreshed via NOTIFY. pHash lookup uses a BK-tree or multi-index hashing for Hamming-distance queries.

---

## 12. Per-group settings

### 12.1 Shape

Settings are a versioned JSON document per group (validated against a Go struct and JSON Schema), plus separate tables for large lists. **Presets** cover 90% of groups, and each field can override the preset.

```jsonc
{
  "preset": "standard",                // relaxed | standard | strict | ask_admins | custom
  "mode": "enforce",                   // shadow | enforce | paused
  "silent": true,                      // §9.7: nothing is posted in the group

  "enforcement": {                     // §8.3
    "mode": "auto",                    // auto | ask_delete_first | ask_before_action  (ask_* never auto-ban)
    "established": { "min_days": 7, "min_clean_messages": 20 },   // established members are never auto-banned
    "ban": { "rejoin_block": true },   // ban = kick + rejoin block
    "autoban_matrix": {                // §9.2: spam from new/unknown senders, per type ("known" = CERTAIN, §7.0)
      "text":      { "action": "auto_ban", "require_source": ["known"], "else": "ask" },
      "links":     { "action": "auto_ban", "require_source": ["known"], "else": "ask" },
      "images":    { "action": "auto_ban", "require_source": ["known"], "else": "ask" },
      "videos":    { "action": "auto_ban", "require_source": ["known"], "else": "ask" },
      "audio":     { "action": "ask" },
      "stickers":  { "action": "auto_ban", "require_source": ["known"], "else": "ask" },
      "documents": { "action": "auto_ban", "require_source": ["known"], "else": "ask" },
      "contacts":  { "action": "auto_ban", "require_source": ["known"], "else": "ask" },
      "channel_shares": { "action": "auto_ban", "require_source": ["known"], "else": "ask" },
      "group_invites":  { "action": "auto_ban", "require_source": ["known"], "else": "ask" },
      "polls":     { "action": "ask" },
      "locations_events": { "action": "ask" },
      "status":    { "action": "auto_ban", "require_source": ["known"], "else": "ask" },
      "view_once": { "action": "ask" }
    }
  },

  "uncertain": { "delete_while_pending": false },   // §7.0: still-uncertain messages stay visible until admins decide
  "policy": {                          // §9.3 rule-breaking (not spam)
    "action": "delete_only",           // delete_only | ask | ban   (default: silent delete, no ban, no strike)
    "repeat": null                     // e.g. { "count": 3, "window": "168h", "then": "ask" }
  },

  "review": {                          // §9.5
    "destination": "dashboard",        // dm | group | dashboard   (dashboard until an admin picks one)
    "reviewers": ["<lid>", "<lid>"],   // used when destination = dm (each must opt in once)
    "review_group": "1203...@g.us",    // used when destination = group (must not be a protected group)
    "send": { "uncertain": true, "appeals": true, "alerts": true, "daily_digest": false },
    "timeout": "6h",
    "on_timeout": "keep_deleted_no_ban",   // keep_deleted_no_ban | delete_no_ban | auto_ban (not in ask modes) | dismiss
    "reactions": { "ban": ["✅", "👍"], "not_spam": ["❌", "👎"] },
    "quiet_hours": "00:00-07:00", "language": "ar", "blur_media": false
  },

  "content": {                         // §9.3 per-type policy: allow | block | block_for_new | block_for_untrusted
    "links": "allow",
    "link_domains": { "allow": ["youtube.com"], "block": [] },
    "group_invites": "allow", "channel_shares": "allow", "contacts": "allow",
    "images": "allow", "videos": "allow", "stickers": "allow", "documents": "allow",
    "audio": "allow", "polls": "allow", "locations": "allow", "events": "allow",
    "view_once": "allow", "forwarded_many_times": "allow",
    "status_shares": "allow", "status_mentions": "allow", "group_status_posts": "allow",
    "document_extensions_block": ["apk", "exe", "scr", "bat", "js"]
  },

  "detection": {
    "thresholds": { "review": 0.60, "delete": 0.85, "ban": 0.95 },
    "ai": { "enabled": true, "vision": false, "daily_budget_calls": 200,
            "spam_confidence": 0.90, "not_spam_confidence": 0.90 },        // the AI verification (§7.0)
    "certain": { "simhash_max_distance": 3, "minhash_min_jaccard": 0.9, "phash_max_distance": 4 },  // tight; operator-only
    "network_intelligence": true,       // share and consume cross-group reputation
    "languages": ["ar", "en"]
  },

  "new_members": {                     // §8.2
    "just_joined": { "enabled": true, "weight": 0.8, "window": "24h" },   // invisible scoring signal
    "screen_on_join": true,
    "join_approval_autopilot": false
  },

  "ladder": { "enabled": false, "window": "168h", "steps": [ ... ], "warn_via": "none" },  // §9.3, optional
  "mute": { "flood_escalate_to_ban": true },                                             // only with the ladder
  "appeals": { "notify_removed": false },   // DM removed people an appeal link (off: ban risk)
  "compromised": {                     // §8.7
    "enabled": true,
    "min_signals": 2,                  // signals from the family needed (1 if combined with a known-spam match)
    "restrict_while_pending": false,   // optional: auto-delete their links/media/forwards until admins decide
    "restrict": ["links", "media", "forwards"],
    "scope": "all_groups",             // all_groups | this_group
    "admins": "alert_only"             // alert_only | delete_known_spam
  },
  "raid": { "enabled": true, "auto_lockdown_minutes": 15, "lockdown_when_backlog_seconds": 60,
            "reopen_when_visible_clean": true },
  "allowlist": { "members": ["<lid>"], "domains": [], "fingerprints": [] },
  "blocklist_refs": ["global:crypto", "global:adult", "group:custom"],
  "learning": { "enabled": true, "auto_learn_min_score": 0.98, "share_to_global": true },  // §7.5
  "network": {                         // §9.6 shared ban list
    "contribute": true,                // share this group's confirmed-spam bans
    "enforce": "org",                  // off | org | all   (in ask modes, incoming network bans become review items)
    "cleanup_delete_messages": true,   // delete their recent spam-matching messages here
    "out_of_scope_bans": "risk_signal",// risk_signal | ignore  (bans from outside the enforce scope)
    "send_invite_on_unban": true
  },
  "intel": {                           // §7.6 identity priors (never enough on their own)
    "use_priors": true,
    "number_prefix_signal": true,      // country-code / prefix clusters: weak signal, admins can turn off
    "picture_and_name_reuse": true
  },
  "limits": { "max_bans_per_hour": 10 }
}
```

Every leaf has a **dotted key** (`enforcement.autoban_matrix.videos.action`, `review.timeout`, `new_members.just_joined.enabled`, …). The website, the `.set`/`.get` commands (§13.7) and the audit log all use these same keys, so anything the website can change, a command can change too.

### 12.2 Presets

All presets share the same defaults: **silent**; spam deleted instantly for everyone; established members, hacked accounts and anything the matrix says `ask` go to admins; policy violations deleted silently with no ban; ladder off; "just joined" signal on; no visible new-member restrictions. They differ in the **auto-ban matrix** (§9.2, full grid there) and a few extras:

| Preset | Auto-ban matrix | Extras | Good for |
|---|---|---|---|
| **Relaxed** | mostly `delete_only`; invites, channels, contacts, links, documents = `ask` | AI on for ambiguous cases | big, trusted communities that never want automatic bans |
| **Standard** (default) | auto-ban on CERTAIN (`known`) evidence for most types, otherwise `ask`; audio, polls, locations, view-once = `ask` | AI verification on | most groups |
| **Strict** | auto-ban on CERTAIN evidence for every type except view-once; AI-verified evidence goes to `ask` until it has a track record (§9.2), and then Strict suggests adding `ai` | lower `T_delete` (0.80); suggests opt-in content rules (links allow-list only, invites/channels blocked) and join-approval autopilot | buy/sell, education, announcement groups |
| **Ask admins** | everything `ask`; `enforcement.mode = ask_delete_first` (variant (a)); can be switched to (b) `ask_before_action` | no auto-bans at all, including incoming network bans | groups that want a human behind every ban |

A **delete-only** setup is just any preset with every matrix cell set to `delete_only` (website: "Never ban automatically, just delete").

**New groups start by asking the admins** (maintainer decision of 2026-10-18). A group with no stored settings starts from the defaults with `enforcement.mode = ask_before_action`: the message stays up, admins are asked to delete and ban together, and with no decision within `enforcement.ask_before.timeout` (15 minutes) it is deleted without a ban. The operator sets this in `config.yaml` (`new_groups.ask_admins`: `before`, the default; `delete` for `ask_delete_first`; `off` for `auto`). The group's first saved change stores it with the rest of the document; `policy.Defaults()` itself keeps `auto`.

**A preset never turns asking off** (same decision). Switching preset resets the matrix and the extras, but a group whose mode asks admins keeps its mode unless the new preset is Ask admins, which sets `ask_delete_first`. Only `.askadmins off` (or `.set enforcement.mode auto`) stops asking.

### 12.3 Lists and scopes

Blocklist and allow-list entries live in `rules` with a **scope**: `global` (maintained by the operator, curated packs like "crypto scams", "adult", "fake jobs"), `account` (per bot), or `group`. Each rule has a type (keyword, phrase, regex, domain, URL prefix, phone number, channel JID, group invite, media SHA256, pHash, SimHash), an action class (hard / weighted), a weight, and an enabled flag. Groups subscribe to global packs.

---

## 13. Web dashboard, auth and onboarding

### 13.0 Two ways to control the bot: website and commands

Both are first-class, and they share one settings model (dotted keys, §12.1), one permission check (current group admin) and one audit log.

| | **Website** | **In-chat commands** (§13.7) |
|---|---|---|
| Best for | **Setup** (onboarding, presets, choosing reviewers and the review group), **rich settings** (the auto-ban matrix grid, lists with dry-run previews, thresholds, the optional ladder), **history** (review queue and decisions, audit log, stats and charts), operator tasks | **Day-to-day control** without leaving WhatsApp: marking spam/not spam, banning with a reason, lockdown, switching mode, preset or auto-ban matrix cells, ask-admins mode, choosing reviewers and the review group, deciding reviews |
| Coverage | everything | **every setting**: friendly shortcuts for the common ones, `.set <key> <value>` / `.get <key>` for the rest (a simplified form, e.g. the matrix one cell at a time with `.autoban videos ask`) |
| Who | admins of the group (phone login) | admins of the group (WhatsApp identity) |

A non-technical admin can run a group with commands alone. The website is where they go to look back ("why was this person kicked?") or to fine-tune.

### 13.1 Tech choice

**Decided later; the API comes first.** The website will be a **separate frontend app** (React, Svelte or Next.js, to be chosen when the full version is built), talking to the bot's **versioned JSON API (`/api/v1`)**. The Go binary serves the API, and can **optionally serve the static frontend build** from the same origin. What this implies:
- **The API is a first-class contract.** It's described by an **OpenAPI spec** in the repo, and the commands and the website share the same settings keys and permission checks (§13.0). Third-party tools can use it too.
- **Same-origin by default.** Serving the frontend build from the Go binary (or behind the same reverse proxy) avoids **CORS** entirely. If a deployer hosts the frontend on another origin, CORS must be configured explicitly (allowed origins, credentials), and cookies need `SameSite=None; Secure`.
- **Sessions:** server-side sessions in an `HttpOnly` cookie stay the default (simplest and safest for a same-origin web app). Short-lived **API tokens** are available for non-browser clients and other-origin setups. CSRF protection applies to cookie sessions.
- **CI:** a **separate frontend build** (its own package manager and lint/test steps) produces static files that are copied into the image or published as their own artifact.

Phase 1 needs no frontend: everything works by command (§13.7), and the API is built alongside.

The dashboard is **mobile-first** (most admins will open it from WhatsApp on their phone), **RTL-aware**, and **Arabic + English** from day one (assumed; to confirm).

### 13.2 Login: phone number + one-time code via WhatsApp

**Flow (as requested: the bot sends the code):**

1. The user enters a phone number. We normalize it to E.164 (libphonenumber port `nyaruka/phonenumbers`).
2. **Eligibility check before sending anything:** the number must be (a) an admin of at least one group the bot protects (via participant PN, or PN→LID through `GetUserInfo`/`LIDStore`), or (b) an operator. Otherwise we show "This number isn't an admin of any protected group. Add the bot to your group first." and **send nothing**. This stops the bot being abused to DM arbitrary numbers, which is ban risk code 101.
3. Generate a 6-digit code (crypto/rand). Store **only an HMAC hash**, with a 5-minute TTL, at most 5 attempts, bound to the browser via a challenge cookie.
4. Enqueue a P3 "OTP" action. The bot DMs: *"Your AntiSpam login code is 482913. It expires in 5 minutes. If you didn't request it, ignore this message."* The text template varies slightly to avoid identical-message patterns.
5. The user types the code. A constant-time compare, then the session is created.
6. **Rate limits:** per number 3 codes/hour and 10/day; per IP 10/hour; global OTP budget per bot per hour; exponential lockout after failures. fail2ban-friendly log lines on the VM.

**Safer alternative (recommended as the default, with the above as a fallback; open question §21): "message the bot to log in."** The dashboard shows a code and a button that opens `https://wa.me/<botnumber>?text=LOGIN%20482913`. The user taps it and sends. The bot receives an **inbound** DM, checks the sender's number matches, and the browser session (waiting via SSE/polling) is logged in. It's one tap on a phone, proves phone ownership just as well, and the bot never initiates a conversation, so there's zero ban risk from logins. Once a user has messaged the bot, later outbound OTPs to them are also much lower risk.

**Sessions:** an opaque random session ID in an `HttpOnly; Secure; SameSite=Lax` cookie (same-origin default, §13.1), stored server-side (Postgres) with a 30-day sliding expiry. "Log out other devices" is supported. CSRF tokens are used for htmx/form posts.

### 13.3 Permissions

- **Identity = phone number (+ resolved LID).** A dashboard user can manage group G if, at request time (from the participant cache, which `events.GroupInfo` Promote/Demote keeps fresh), they are an admin or superadmin of G.
- Every write re-checks admin status (cache, with a forced refresh if the cache is older than 10 minutes). If an admin is demoted in WhatsApp, they lose access immediately.
- Roles inside a group: **owner (superadmin)** can change everything, including network-intelligence sharing and the review destination. **Admin** can change settings, decide reviews and appeals, and (unless the owner locks it) choose reviewers. Optional per-group setting: "only the owner may change settings" for large groups with many admins.
- **Operator** role: **whoever deploys the instance** (for the user's own deployment, that's the user). Configured by phone number in env (`OPERATOR_PHONES`). Sees all groups of *that deployment*, global rule packs, the spammer database, bot health and pairing, queue, budgets, kill switch.
- **Audit log** of every settings change (who, when, diff), visible to that group's admins.

### 13.4 Dashboard UX (pages)

1. **My groups:** cards with protection status (Enforcing / Shadow / Paused / ⚠ bot not admin), today's stats (deleted, banned, sent to admins), and pending reviews.
2. **Group overview:** a timeline of recent detections with one-line explanations, "Undo"-style buttons where possible (restore member / mark not spam), and charts (spam by type, by hour).
3. **Protection settings:** a preset picker with a plain-language description of each, then "Customize": the **auto-ban grid** (one row per content type: Auto-ban / Ask admins / Delete only / Allow, with an "Advanced" evidence column), an **always-ask-admins** switch with its two variants, **content rules** in everyday language (*"Videos: Allowed / Not allowed (deleted silently)"*), the "just joined" signal toggle, and the optional ladder. Every change shows a **preview** ("Last week this would have changed 5 decisions").
4. **Lists:** allowed members, allowed/blocked domains, blocked words with a test box, subscribed global packs.
5. **Reviews & notifications settings** (§9.5): destination picker (DM reviewers / review group / website only) with plain-language explanations. For DM: a list of current admins with checkboxes, each showing an opt-in status (✔ active / ⏳ waiting for opt-in, with a "resend opt-in link in group" button). For a review group: the current review group, a warning if it has non-admin members, and instructions ("send `.reviewgroup here` in that group"). Also: what to review, confirm-before-kick, timeout and fallback, quiet hours, reaction mapping, a preview of a sample review message.
6. **Review queue:** open items (swipe-style on mobile: Ban / Not spam / Delete only), evidence shown, personal data masked. The same items as in WhatsApp, and a decision in either place closes it in both.
7. **Review history:** every decided item with who decided, how (reaction / reply / website / command / timeout), when, the outcome, and any later overturn. Filter by group, decider and outcome.
8. **Spammer intelligence** (§7.6):
   - **Group admins:** a list of spammers who were in their groups, with name history, picture (hash match or thumbnail if enabled), join method and who added them, join-to-spam time, the messages and evidence from *their* groups, network-ban status ("also banned in 3 groups"), and buttons to unban or mark as a false positive.
   - **Operator:** global search (name, number, LID, domain, picture similarity); full profile timeline; **campaign view** (graph of accounts ↔ domains/numbers/channels/pictures, with sizes and activity); number-prefix and timing patterns with their lift, weight, overturn counts and an enable/disable switch; the network-ban queue (candidates awaiting operator approval, "identity pending" watches) and the full audit trail.
9. **Learned patterns** (§7.5): what the group's learning has added (fingerprints, domains, numbers, corpus entries), where each came from (which review/ban, which admin), hit counts, and a one-click **retract**.
10. **Appeals.**
11. **Activity/audit log.**
12. **Operator:** bot numbers (pair via QR or pairing code, health, ban status), queue depth, AI spend, global packs, global promotion queue for learned patterns, poisoning alerts, kill switch.

### 13.5 Notifications to admins

Notifications (raids, the bot losing admin, breaker open, digests, review items) go to the group's **review destination** (§9.5), so admins configure one place. "Website only" groups see them as a banner on the website.

### 13.6 Onboarding (non-technical path, silent in the group)

Nothing is posted in the group at any point (§9.7). Onboarding happens in a DM the admin starts, and on the website.

1. **Start with the bot** (landing page or setup guide): a `wa.me/<bot>?text=START` button. The admin taps it and sends, which opens an **inbound** DM thread. The bot replies with three steps and a website link. The bot's profile "about" text also says *"Message me 'start' to set up"*, for admins who find the bot some other way.
2. **Add the bot** to the group (the bot account's privacy setting "who can add me to groups = everyone" is set at pairing). *Alternative:* send the bot a group invite link in that DM. The bot joins via `JoinGroupWithLink` only if the sender's number is an admin of that group (checked after joining; it leaves if not) and within per-bot join limits.
3. **Make the bot admin.** The bot detects the promotion through `events.GroupInfo.Promote`. The admin who added or promoted it (from `GroupInfo.Sender`) is told by DM: *"Protection is on in 'Riyadh Cars Market' (Standard, silent). Uncertain cases go to the website for now. Reply `.review dm` to get them here, or send `.reviewgroup here` in a separate admins' group."* **Inbound-first:** the DM is sent only if that admin has an open thread with the bot (step 1, a login, or any earlier message). Otherwise the bot waits silently. The group shows as "waiting" on the website, and the admin is told the next time they message the bot or log in.
4. **Not admin yet?** No message in the group. The group shows as "⚠ bot is not admin" on the website, and in the DM thread if there is one (at most one reminder per day).
5. **Choose where reviews go** (DM or review group), by command or on the website. Until then, reviews go to the website only.
6. **Optional first run in shadow mode** for 24 h, with a summary sent to the review destination ("I would have deleted 6 messages and banned 2 people; review: link").
7. **Health nudges** (bot lost admin, many unanswered reviews): to the review destination, the admin's DM thread, or a website banner. Never in the protected group.

### 13.7 In-chat commands (admins only)

Commands give **full day-to-day control without leaving WhatsApp** (§13.0). Prefix `.` (configurable; Arabic aliases supported, e.g. `.حالة` for `.status`). The bot ignores commands from non-admins.

**Where commands work:**

- in the protected group itself: the context is that group. The bot **acts on the command, deletes the command message** (so members don't see moderation chatter), and replies **by DM**. If the admin has never messaged the bot, that first reply is a direct response to a command they just sent: it's a single short DM, rate-limited to one per admin per day until they reply. That's the one small exception to "inbound-first", and it's noted in the setup guide;
- in the review group: the context is the group named in the item being replied to, or picked with `.use <group name>`;
- in a DM with the bot: the admin picks a group with `.use <group name>` (or `.groups` to list them). The DM context is remembered for 1 hour.

| Command | Effect |
|---|---|
| **Moderation** | |
| reply to a message with `.spam` | delete it and **ban** the sender (an admin decision, so it's allowed even for established members), record it as *confirmed spam* (learning, §7.5) |
| reply with `.notspam` / `.ok` | restore the member's trust, allow-list the fingerprint, record the false positive, **retract** any learned pattern (§7.5) |
| `.ban @member spam\|other` or reply `.ban spam\|other` | local ban (kick + rejoin block here). With `spam`, it also becomes a network candidate, or an active network ban if the network rule is met (§9.6). The **reason is required** and only `spam` feeds learning or the network. With no reason given, the bot asks "Reason? reply `spam` or `other`" and treats no answer as `other`. |
| `.kick @member [spam\|other]` | kick without a rejoin block |
| `.mute @member 2h`, `.unmute @member` | only when the optional ladder is on (§9.3) |
| `.allow @member` / `.unallow @member` | member allow-list |
| `.blacklist add\|remove <number> [<number> …] [reason]`, `.blacklist list`, `.blacklist check <number>` | manual phone-number blacklist (§9.8): bans the number if it's a member, keeps them out, and treats the number in content as known spam |
| `.clear @member` (or reply `clear` to a hacked-account review) | close a possibly-hacked review without a ban, lift any restriction (§8.7) |
| `.restrict @member [24h]` / `.restricted` | manually restrict a member (links/media/forwards auto-deleted) / list restricted members |
| `.unban <number or reply>` | lift a local ban. For a network ban, this lifts it network-wide if this group is its only source; otherwise it opens an unban request to the operator (§9.6) |
| `.network off\|org\|all` / `.network` | shared ban list scope for this group, or show status (network bans affecting this group, pending identity matches) |
| `.lockdown 15m` / `.unlock` | manual raid mode |
| `.undo` | reverse the bot's last action in this group where possible (unban/re-invite, restore trust) |
| **Reviews** (§9.5) | |
| `.review dm\|group\|dashboard` | choose the review destination |
| `.reviewers add @a @b` / `.reviewers remove @a` / `.reviewers` | choose who gets review DMs (each gets a one-time opt-in link) |
| `.reviewer on\|off` (in DM) | opt in or out of receiving review DMs |
| `.reviewgroup here` (sent *in* the review group) / `.reviewgroup off` | set or clear the review group |
| `.review timeout 6h keep\|delete\|ban\|dismiss` | timeout and fallback (`ban` refused in ask modes) |
| `.askadmins off\|delete\|before` | always-ask-admins mode: off (auto), (a) delete now and ask about the ban, (b) ask before doing anything (§8.3) |
| `.autoban <type> auto\|ask\|delete\|allow [sources]` / `.autoban` | set one matrix cell, e.g. `.autoban videos ask`, `.autoban links auto known` (or `known,ai` after the track record); or show the grid (§9.2) |
| `.review K7Q2 kick` / `.pending` | decide an item by ID, or list open items |
| **Settings** | |
| `.status` | current preset, mode, review destination, today's stats |
| `.mode shadow\|enforce\|pause` | switch mode |
| `.preset relaxed\|standard\|strict\|ask` | switch preset (resets the matrix to that preset) |
| `.block videos`, `.allow stickers`, `.blocknew links` (opt-in) | content **policy** shortcuts: violations are deleted silently, no ban (§9.3) |
| `.policy delete\|ask\|ban` | what happens on a policy violation (default `delete`) |
| `.silent on\|off`, `.ladder on\|off`, `.newsignal on\|off` | silent mode (§9.7), optional ladder (§9.3), invisible "just joined" signal (§8.2) |
| `.domain allow\|block example.com`, `.word block <phrase>` (the bot replies with a dry-run count before saving), `.word unblock <phrase>` | lists |
| `.set <key> <value>` / `.get <key>` / `.settings` | **any** setting by its dotted key (§12.1), e.g. `.set new_members.just_joined.window 48h`. `.settings` gives a short summary plus a link to the website. Values are validated with the same schema as the website. |
| **Other** | |
| `.groups` / `.use <name>` | list your groups / set the DM context |
| `.login` | the bot replies by DM with a website login link (never in the group) |
| `.help [topic]` | short help |

Every command that changes something is written to the audit log with the same dotted keys as website changes. Bot replies to commands are concise and rate-limited. Nothing is ever replied in the protected group; replies go by DM or in the review group.

---

## 14. Data model

PostgreSQL 16+. whatsmeow's own tables live in schema `whatsmeow` (via `sqlstore`). App tables live in schema `app`. JIDs are stored as text. Personal data is kept minimal and subject to retention limits.

```sql
-- bot numbers
bots(id, jid, lid, display_name, status /*active|paused|banned|unpaired*/, lease_owner, lease_until,
     rate_profile jsonb, created_at)

-- groups and settings
groups(jid PK, bot_id FK, name, topic, size, addressing_mode, bot_is_admin, joined_at,
       status /*pending_admin|active|left*/, last_info_refresh)
group_settings(group_jid PK, version int, doc jsonb, updated_by, updated_at)   -- + history table
group_settings_history(group_jid, version, doc, updated_by, updated_at)

-- people (cross-group identity) and per-group membership
persons(id PK, lid UNIQUE NULL /*primary; set as soon as known*/, pn NULL, pn_country NULL, push_name, verified_name, profile_pic_id, profile_pic_phash,
        about_text_hash, business jsonb, global_reputation float, first_seen, last_profile_scan)
members(group_jid, person_id, role /*member|admin|superadmin*/, joined_at, first_seen,
        msg_count, clean_count, trust float, strikes int, last_strike_at, muted_until,
        allowlisted bool, established bool /*routing only, §8.1*/, restricted bool, restricted_since NULL, restricted_until NULL,
        restricted_reason /*compromised|manual*/, PRIMARY KEY(group_jid, person_id))

-- ingestion
inbox(id bigserial, bot_id, chat, msg_id, sender, kind, raw bytea /*proto*/, info jsonb,
      received_at, processed_at NULL, UNIQUE(chat, msg_id, edit_seq))     -- short retention (e.g. 72h)
message_index(group_jid, msg_id, sender_person, ts, fingerprint, simhash bigint, url_domains text[],
              media_sha256 bytea[], verdict, PRIMARY KEY(group_jid, msg_id))
              -- NO text. Normal messages: 7 d (edits → original ID, cleanup, delete-window logic, dry-runs);
              -- rows for spam/actioned messages: 30 d

-- detection and actions
detections(id, group_jid, msg_id, person_id, score, tier_reached, signals jsonb, verdict /*spam|policy|clean*/,
           confidence_source /*known|rules|ai*/, route /*auto_ban|ask|delete_only|allow|policy|none*/,
           rule_version, action_plan jsonb, mode /*shadow|enforce|network_rescan*/, network_ban_id NULL, evidence_ref, created_at)
actions(id, bot_id, group_jid, kind, payload jsonb, priority smallint, status, attempts,
        not_before, deadline, dedupe_key UNIQUE, detection_id, last_error, created_at, done_at)
strikes(id, group_jid, person_id, detection_id, weight, created_at, expires_at)   -- used by the optional ladder and policy.repeat
group_bans(id, group_jid, person_id, reason /*spam|other|policy*/, decided_by /*auto|admin|network*/,
        decider_person NULL, detection_id NULL, review_id NULL, network_ban_id NULL, rejoin_block bool,
        active bool, created_at, lifted_by NULL, lifted_at NULL)   -- ban = kick + rejoin block

-- rules, fingerprints, reputation
rules(id, scope /*global|bot|group*/, scope_id, pack, type, pattern, norm_pattern, action_class,
      weight, enabled, created_by, hits, overturns, created_at)
fingerprints(kind /*sha256|phash|simhash|channel|invite|number|url*/, value, label /*spam|allow*/,
             source /*admin|auto|feed*/, scope, hits, first_seen, last_seen, PRIMARY KEY(kind, value, scope))
domains(etld1 PK, reputation float, sources jsonb, registered_at, checked_at)
link_cache(key PK, kind, result jsonb, expires_at)
media_cache(sha256 PK, phash, ocr_text_hash, labels jsonb, checked_at)
ai_cache(key PK, model, prompt_version, result jsonb, created_at)
ai_usage(day, group_jid, calls, input_tokens, output_tokens, cost_micros)

-- reviews and notifications (§9.5); the destination config lives in group_settings.doc.review
reviewers(group_jid, person_id, added_by, added_at, dm_opt_in_at NULL, active bool,
          PRIMARY KEY(group_jid, person_id))                    -- chosen DM reviewers + opt-in state
review_targets(id, group_jid, kind /*dm|group|dashboard*/, chat_jid NULL, priority smallint,
          active bool, last_error)                              -- resolved destinations incl. fallback chain
reviews(id, short_code UNIQUE /*K7Q2*/, group_jid, kind /*ban_decision|uncertain|hacked|ask_before|network_incoming|appeal|alert*/,
        detection_id NULL, appeal_id NULL, subject_person_id, proposed_action jsonb,
        status /*pending|decided|timed_out|cancelled*/, deadline, on_timeout,
        decided_by_person NULL, decision /*ban|not_spam|delete_only|clear|mute(ladder)|ignore*/, method
        /*reaction|reply|website|command|timeout*/, decided_at, outcome_posted_at)
review_messages(review_id, bot_id, chat_jid, msg_id, sent_at, editable_until /*sent_at+20m*/,
        outcome_edited bool, PRIMARY KEY(chat_jid, msg_id))     -- maps reactions/replies to reviews
review_votes(id, review_id, voter_person_id, method, raw /*emoji or text*/, valid bool,
        invalid_reason /*not_admin|late|unknown_keyword*/, at)  -- full audit, incl. late/invalid votes
appeals(id, group_jid, person_id, detection_id, message, status, review_id, decided_at)
manual_actions(id, group_jid, actor_person_id, target_person_id, kind /*ban|kick|unban|restrict|mute*/,
        reason /*spam|other|unspecified*/, via /*command|website|whatsapp_ui*/, at)

-- learning (§7.5)
corpus_entries(id, kind /*message|profile*/, label /*spam only*/, scope /*group|global*/, group_jid,
        status /*provisional|active|retracted|promoted|expired*/, source /*review|command|auto|import*/, import_batch_id NULL, source_review_id, source_action_id,
        decided_by_person NULL, text_raw_redacted, text_norm, simhash bigint, minhash bytea,
        keywords text[], urls text[], domains text[], number_hashes bytea[], channel_jids text[],
        invite_codes text[], media_sha256 bytea[], phashes bigint[], profile jsonb, biz jsonb,
        confidence float, hits int, last_hit_at, created_at, expires_at)
corpus_confirmations(pattern_key, group_jid, admin_person_id, corpus_entry_id, at,
        PRIMARY KEY(pattern_key, group_jid))                    -- drives the ≥3-unrelated-groups promotion rule
learned_rules(rule_id FK rules, corpus_entry_id, group_jid, created_at)   -- so a retraction removes them
admin_reputation(person_id, group_jid, decisions, overturned, contributions_today, flagged bool)

-- one-time import / bootstrap (§7.8)
import_batches(id, file_name, file_sha256, format /*jsonl|csv*/, scope /*instance|group*/, group_jid NULL,
        started_by, started_at, finished_at, stats jsonb /*imported, merged, spam, ham, clusters, holdout*/)
                                                   -- corpus_entries carry import_batch_id, so an import can be removed
eval_corpus(id, source /*import_holdout|confirmed_live*/, import_batch_id NULL, cluster_key, text_norm, label /*spam*/,
        category, kind, language, added_at)        -- hold-out, split by cluster/campaign (§7.8, §18)

-- manual phone-number blacklist (§9.8)
number_blacklist(id, scope /*group|instance*/, group_jid NULL, pn_e164, pn_hash /*keyed hash for content matching*/,
        person_id NULL /*linked once PN→LID resolves*/, on_whatsapp bool NULL, reason, added_by_person, added_at,
        removed_by_person NULL, removed_at NULL, UNIQUE(scope, group_jid, pn_e164) WHERE removed_at IS NULL)

-- compromised accounts (§8.7)
compromise_events(id, person_id, detected_at, signals jsonb, groups text[], trigger_detection_id,
        status /*open|cleared|confirmed_spammer*/, cleared_by_person NULL, cleared_at NULL, method)

-- spammer intelligence (§7.6). Rows exist only for confirmed spammers.
spammer_profiles(id, person_id UNIQUE, status /*active|unbanned|expired*/, first_confirmed_at,
        last_seen_at, confirmations int, groups_count int, campaign_id NULL, risk float,
        expires_at /*last activity + 12 months*/)
identities(id, person_id, kind /*lid|pn|other*/, value, country_code NULL, number_prefix NULL,
        source /*sender_alt|participant|event|lid_store|dm|self_vcard|usync*/, first_seen, last_seen,
        valid_to NULL)                              -- all persons; unique (kind, value) WHERE valid_to IS NULL
person_merges(id, from_person, into_person, reason, at)   -- PN-only record merged into its LID person (§7.7)
profile_snapshots(id, person_id, taken_at, push_name, push_name_norm, about_text, picture_id,
        picture_phash bigint, picture_thumb_enc bytea NULL /*off by default*/, verified_name,
        business jsonb, status_text NULL)          -- spammers only; keep the last 10 per person
name_history(person_id, name_norm, raw, first_seen, last_seen, PRIMARY KEY(person_id, name_norm))
membership_history(person_id, group_jid, joined_at, join_method /*invite_link|added|approved|preexisting*/,
        added_by_person NULL, approved_by_person NULL, first_message_at NULL, join_to_first_msg_s NULL,
        messages int, left_at NULL, left_reason /*left|kicked|network_ban|banned_local*/)
                                                    -- captured for all members; kept long-term only for spammers
activity_stats(person_id PK, hour_histogram int[24], median_interval_s, burst_max_groups_10m,
        msgs_total, updated_at)
campaigns(id, label, status /*active|dormant|retracted*/, accounts int, artifacts int,
        first_seen, last_seen)
campaign_links(campaign_id, node_kind /*account|domain|number_hash|channel|phash|simhash|invite*/,
        node_value, evidence_count, first_seen, last_seen, PRIMARY KEY(campaign_id, node_kind, node_value))
intel_patterns(id, kind /*number_prefix|picture|name|timing*/, key, spammers int, base_members int,
        lift float, weight float, enabled bool, overturns int, computed_at)

-- network bans (§9.6)
organisations(id, name, created_by)            -- website-defined organisation
group_orgs(org_id, group_jid, basis /*same_owner|community|admin_link*/, linked_by, PRIMARY KEY(org_id, group_jid))
network_bans(id, person_id, origin_group, status /*candidate|active|revoked*/, basis jsonb /*quorum branch + evidence*/,
        created_by_person, approved_by_operator NULL, created_at, revoked_by, revoked_at, revoke_reason)
network_ban_confirmations(network_ban_id, group_jid, admin_person_id, method, detection_id, at)
network_actions(id, network_ban_id, group_jid, bot_id, kind /*kick|delete|reject_request|watch|override*/,
        action_id NULL, status /*planned|done|skipped|failed|too_old*/, detail, at)
identity_watch(network_ban_id, kind /*lid|pn*/, value, group_jid NULL)   -- "identity pending" matches
intel_access_log(id, viewer, person_id, scope /*global|group*/, group_jid NULL, at)  -- read audit
audit_log(id, actor /*phone|system|operator*/, group_jid, action, diff jsonb, at)

-- dashboard auth
dash_users(id, phone_e164 UNIQUE, person_id, is_operator, created_at, last_login)
dash_sessions(id_hash PK, user_id, created_at, last_seen, expires_at, ua, ip_prefix)
otp_challenges(id, phone_e164, code_hmac, method /*outbound|inbound*/, browser_binding,
               attempts, expires_at, consumed_at)
```

Indexes: `identities(kind, value)`, `identities(person_id)`, `network_bans(person_id, status)`, `identity_watch(kind, value)`, `profile_snapshots(picture_phash)` (BK-tree in memory for Hamming search), `actions(bot_id, status, priority, not_before)`, `detections(group_jid, created_at DESC)`, `message_index(simhash)` (plus an LSH-band table if needed), `members(person_id)`, `reviews(status, deadline)`, SimHash LSH-band table for `corpus_entries` (or an in-memory index), GIN on `corpus_entries(domains)`, `(number_hashes)` and `(channel_jids)`.

**Postgres:** the plain official image (`postgres:16`); no extensions are required.

**Retention defaults:** raw inbox 72 h; message fingerprints/metadata 7 d for normal messages and 30 d for spam/actioned ones (no text in either); spammer profiles while the network ban is active, plus 12 months after last activity (details §17.1); evidence snapshots (redacted text + thumbnail) 30 d; corpus raw text 180 d, corpus fingerprints 180 d after the last hit; reviews, votes, detections and audit 180 d; aggregates forever. All configurable.

---

## 15. Scalability and multiple bot numbers

### Capacity model: one bot number

**Compute is not the bottleneck.** whatsmeow uses tens of MB of RAM per number. The cheap tiers take ~0.1–2 ms of CPU per message, so even 200 messages/s fits in one core of the 2 vCPU ARM VM. The AI runs only for the uncertain few percent.

**The real budget is WhatsApp writes.** One write is one action: a delete, one kick call (up to `max_kick_batch` members), a lockdown toggle, a review message or a notice. The bot's **ceiling** (§10.3) is fixed and never rises on its own:

| | Writes/min | Writes/hour | Writes/day |
|---|---|---|---|
| Default ceiling | 20 (+ burst of 10) | 1,200 | 28,800 theoretical maximum |
| Planning figure (keep ~25% headroom for waves) | ~5 on average | ~300 | **~7,500** (also the daily alert level) |

**Demand per group** is mostly *spam messages to delete*, because kicks are batched and lockdowns replace mass deletes:

| Group type | Spam msgs/day (typical → bad day) | Writes/day |
|---|---|---|
| Quiet 200-member group | 0–5 → 30 | ~5 → ~40 |
| Busy 1,000-member community group | 10–50 → 300 | ~60 → ~150 (lockdown caps a bad day) |

So **one number at the default ceiling comfortably covers on the order of 100 busy large groups, or several hundred quiet ones, on a normal day, with headroom for waves.** A bad day in a few groups at once is absorbed by **lockdown and batching**, not by a higher rate. These figures are planning estimates, to be replaced by real metrics (`writes_per_group_per_day`) after the spike. Deployments that outgrow one number add a second one (Phase 6) rather than raising the ceiling. Other limits worth knowing:

- WhatsApp may limit how many groups one account can be in (not published; we'll measure it).
- Sends to very large groups fan out to every member's devices, which costs time, so review messages go to small review groups or DMs, not big groups.

**Worked example: a spam wave in a 1,000-member community group.** A leaked invite link brings 30 spam accounts, each posting 5 messages within 2 minutes (150 spam messages). Defaults: ceiling 20/min + burst 10, `max_kick_batch` 8, `max_kicks_per_hour` 10.

| Time | What the bot does | Writes |
|---|---|---|
| 0–10 s | The first few spam messages are deleted at P0 from the burst allowance. The raid detector sees the join burst and duplicate content, and the projected backlog (over 100 deletes ≈ 5+ min) is far beyond 60 s, so it switches the group to **admins-only** (announce) straight away. **New spam stops here.** | ~4 + 1 |
| 10–60 s | Join approval on, invite link reset. Bans: the raid accounts are new and the evidence is `known`/`rules`, so under the Standard matrix they're auto-banned. **10 accounts in 2 calls** (8 + 2) up to the hourly cap; the other 20 go to the review destination as one "Ban the remaining 20 raid accounts? ✅" item, and an admin's ✅ releases them in 3 more calls. One alert. | 2 + 2 + 1 (+3 after ✅) |
| 1–~8 min | Deletes the remaining ~145 spam messages **newest first** (the visible part of the chat clears in the first minute or two) at 20/min, while nobody can post | ~145 |
| ~8 min | Backlog clear: reopen the group; a short "spam wave removed" summary goes to the review destination (nothing in the group) | 1–2 |

Total ≈ 160 writes, about 2% of the daily alert level. Members saw spam for about 10 seconds, because the **lockdown** did the protecting, not the delete rate. For a much bigger wave (e.g. 1,000 messages), deleting at the ceiling takes ~50 minutes. The group stays locked meanwhile, the newest (visible) spam goes first, and admins can reopen earlier with `.unlock` once the visible part is clean (`raid.reopen_when_visible_clean`, default on: the bot reopens after the newest ~50 spam messages are gone). Network-ban cleanup for the 30 accounts in other groups runs afterwards at lower priority (§9.6).

### Multiple bot numbers (optional, Phase 6)

Not required. One number runs everything above. Extra numbers add redundancy (a standby during a temp ban) and more total write capacity for very large deployments, which is safer than raising one number's ceiling.


- `bots` table; each group is **assigned** to one bot (the one that is admin there). If two bots are in the same group, one is the **primary** (acts) and the other is a standby (reads only, takes over if the primary loses admin or gets banned).
- **Failover:** when a bot is temp-banned, its groups' actions are re-routed to a standby bot in the same group if there is one. Otherwise they queue until the ban expires (respecting deadlines).
- **Spread load:** onboarding suggests the least-loaded number. The operator can migrate groups (add the new bot, make it admin, flip primary).

### Multi-node (later)

- **Connector lease:** each bot number is owned by one process via a Postgres row lease (`lease_owner`, `lease_until`, renewed every 10 s) or a `pg_try_advisory_lock(bot_id)`. Another process never connects with the same session (avoids `StreamReplaced`).
- Analysis workers are stateless and pull from `inbox`. The web server is stateless.
- When nodes > 1: add Valkey/Redis for shared hot caches and rate-limit buckets (or keep buckets inside the connector process, since only it writes for its bot).
- Postgres scaling: partition `inbox`, `detections` and `message_index` by month. This is fine into the millions of messages per day.

### Cross-group intelligence (within one deployment)

Because one deployment protects many groups, a campaign seen in group A immediately raises scores in group B (same SimHash, sticker hash, channel, number or domain) as a *weighted* signal. It becomes a hard global rule only after the promotion rules in §7.5. This is the bot's biggest structural advantage over per-group human moderation. It's opt-in per group. Privacy: ordinary messages are never shared, only fingerprints and verdicts. Promoted global corpus entries (confirmed spam seen in ≥ 3 unrelated groups) also carry their *redacted* spam text, which the similarity tier and LLM examples need, and only from groups that opted in.

**Scope:** network bans, the spammer database, the learned corpus and "global" rules are all scoped to **one deployment** by default. Nothing leaves an instance.

**Optional cross-instance sharing (opt-in, off by default):** an operator can subscribe to, or publish, **shared feeds**:
- **Rule-pack feeds:** signed files of keyword/regex/domain rules, like the built-in packs.
- **Fingerprint feeds:** hashed fingerprints of confirmed spam (domains, keyed hashes of advertised numbers, channel JIDs, media/sticker hashes, SimHashes), with no message text, names, photos or member identities.

Feeds are plain files over HTTPS (versioned, signed with the publisher's key). Imported entries count as weighted signals, never hard blocks or network bans, unless the operator promotes them. Network bans and spammer profiles are **never** shared between instances.

---

## 16. Observability

- **Logs:** zerolog JSON (whatsmeow already uses zerolog via `waLog`), with a request/detection ID. Phone numbers are masked in logs. Log level per module.
- **Metrics** (Prometheus `/metrics`, scraped by a Coolify-deployed Prometheus/Grafana or Grafana Cloud free tier):
  - connection: `wa_connected{bot}`, reconnects, keepalive timeouts, `wa_temp_ban_total{code}`;
  - ingestion: messages by kind/group, unknown kinds, undecryptable, inbox lag;
  - detection: verdicts by tier/label, tier latency histograms, score distribution, rule hit counts;
  - actions: queue depth by priority (unbounded, so watch it), **backlog ETA per group and per bot**, wait time, current effective rate vs. ceiling, slow-down events by cause (429, errors, slow acks, disconnects), daily writes vs. the alert level, kick batch sizes, kicks deferred by the hourly cap, success/failure by error, 429s, breaker state, deletes/kicks per hour, lockdowns triggered by backlog;
  - compromised accounts: events opened, members in restricted mode, time to admin clear, events later confirmed as real spammers;
  - AI: **calls per 1,000 messages** (target ≤ 20–50, i.e. 2–5%), calls by provider/model, latency, tokens, **cost per day/group** (or compute time for local models), cache and cluster-reuse hit rate, invalid-output retries, share of "unsure", budget remaining;
  - quality: review decisions, **overturn rate** (false-positive proxy), appeals upheld;
  - reviews: items opened by kind, time-to-decision, % timed out, invalid votes (non-admin/late), reviewers pending opt-in;
  - learning: corpus entries added/retracted/promoted, near-duplicate (SimHash/MinHash) hit rate, poisoning-guard rejections, weight-change deltas.
- **Health:** `/healthz` (process), `/readyz` (DB + at least one bot connected). Coolify health checks use these.
- **Alerts** (Grafana or a built-in notifier → Telegram/email/webhook): bot disconnected > 2 min, temp ban, breaker open, repeated slow-down events, daily writes above the alert level (7,500 by default), ceiling changed by an operator, queue wait > 60 s, overturn rate spike, AI budget at 80%, "bot lost admin" in many groups.
- **Tracing:** optional OpenTelemetry spans per message through the tiers (useful while tuning, off by default).

---

## 17. Security and privacy

- **Session secrets:** the whatsmeow store holds the account's keys. The Postgres volume is persistent and backed up (encrypted). The DB is never exposed publicly. Coolify secrets go in env vars.
- **Dashboard:** HTTPS via Traefik, HSTS, strict CSP, CSRF protection, server-side sessions, login rate limits, and fail2ban patterns on auth failures. The operator area can additionally be restricted by IP or reached only through the SSH tunnel (like your Coolify panel).
- **SSRF:** URL expansion uses a dialer that blocks private/link-local/metadata IPs, caps redirects and sizes, and runs with timeouts.
- **Untrusted media:** decoded in a bounded worker with size/pixel limits (decompression bombs) and timeouts. External decoders (ffmpeg/tesseract) run as subprocesses with resource limits.
- **Regex DoS:** Go's RE2 is linear time. Admin-supplied patterns also have length limits.
- **Normal messages are never stored long-term or used for training.** What's kept for a normal message is the minimum needed to run:
  - the **raw inbox (72 h)**: crash-safe processing, re-scans for ban cleanup within WhatsApp's ~2-day delete window, and in-memory dry-runs;
  - **`message_index` fingerprints and metadata, no text (7 days)**: mapping edits to the original message, ban cleanup, "too old to delete" logic, and in-memory poisoning checks.

  There is **no ham corpus**, no normal-message few-shot examples, and no training on normal chat. Overturned messages leave only a fingerprint allow-list entry; their evidence text is deleted (§7.5). Optional `ham` records in an import are used in memory only (§7.8).
- **Data minimization:** store fingerprints and metadata, not message content, except confirmed-spam corpus entries (redacted) and short-lived redacted evidence for actions. Phone numbers are shown masked in the UI except to the operator. Data is deleted on group offboarding. A privacy notice is linked in the activation message.
- **AI data:** only uncertain messages go to the AI (§7.4), minimized and redacted. **When the context bundle is on (default), the AI also receives other members' recent messages** (up to N, truncated and redacted), the replied-to message and existing replies, all taken from the 72 h inbox and never stored for this purpose. These go to **whichever AI provider the deployer configured**. With a hosted provider they leave the server; with a local model they don't. Groups can turn the context off (`ai.context: false`) or turn AI off entirely. The privacy notice names the configured provider.

### 17.1 Spammer intelligence and network bans: privacy

**This applies to each deployer.** Every instance is run by its own operator, who is the party responsible for the personal data that instance processes. The project ships privacy-friendly defaults, a template privacy notice and the deletion tooling, but can't take on that responsibility for them. The setup guide says so clearly.

Storing phone numbers, names, profile photos and message content of identifiable people is **personal data processing**. It is likely covered by data-protection law wherever the bot's users or operator are, for example Saudi Arabia's **PDPL** (Personal Data Protection Law), the EU/UK **GDPR** if any members are there, and similar laws elsewhere. *This is a design note, not legal advice. Have it checked for the jurisdictions you operate in before running the network or intelligence features for groups you don't own.* The design follows the usual principles:

- **Purpose and basis:** fraud and spam prevention for the protected groups. Admins accept this when they set up the bot, and the onboarding DM, the bot's profile text and the website link a **privacy notice** that explains what's stored, why, for how long, and how to appeal or ask for deletion.
- **Minimization:**
  - Spammer profiles exist only for *confirmed* spammers.
  - Profile pictures are stored as **perceptual hashes only** by default. An encrypted, small review thumbnail is off by default, and when enabled it's kept for 30 days at most.
  - Phone numbers of third parties in message text are masked (§7.5).
  - Country-code use is a weak signal that can be switched off (§7.6).
- **Retention:**
  - Active spammer profiles: while a network ban is active, plus **12 months after last activity**.
  - Profile snapshots: the last 10 per person.
  - Raw redacted text: 180 days (§7.5).
  - Membership history for non-spammers: 30 days.
  - Expiry jobs delete the data, they don't just hide it.
- **Access control:**
  - The **global** intelligence view (all groups, campaigns, number-prefix patterns) is **operator-only**.
  - Group admins see only spammers who were in *their* groups, and only the data from their groups, plus counts such as "also banned in 3 other groups" (other groups' names only when in the same organisation).
  - Every view of a spammer profile is written to `intel_access_log`. There's no bulk export from the website.
- **What an upheld appeal deletes:**
  - the spammer profile, identities (beyond the minimum still needed for the person's ordinary membership), snapshots, name history, campaign links, activity stats, and corpus entries/learned rules caused by that person;
  - what remains is a minimal audit record: ban ID, decision, dates, and a keyed hash of the identity, so the same mistake can be recognised and not repeated.
  - Deleted messages can't be restored.
- **Deletion requests from the person** (DM "delete my data"): routed to the operator. Data tied to an *active, confirmed* network ban may be kept as long as the ban is justified, reduced to identifiers and fingerprints; the person is told so. Otherwise it's deleted within 7 days.
- **Security:** photos and identity fields are encrypted at rest (column-level encryption with a key held outside the database), and backups are encrypted.

---

## 18. Testing strategy

| Layer | Approach |
|---|---|
| **Normalizers / extractors** | Table-driven tests with real-world evasion samples (zero-width, homoglyphs, Arabic variants, spaced phone numbers). **Go fuzz tests** (`testing.F`) to ensure no panics on hostile input. |
| **Envelope builder** | Fixtures: one serialized `waE2E.Message` per message type (captured in the spike), plus golden-file envelope output. A test fails if a new proto field type appears unhandled (reflection-based coverage). |
| **Analyzers** | Unit tests per analyzer with fixed clocks and fakes for enrichment. |
| **Scoring / decision** | Property tests (all in CI): **only CERTAIN evidence can auto-ban by default**; **heuristics and looser near-duplicate matches never delete or ban on their own** (they go to the AI or admins); **an AI "spam" verdict alone never auto-bans** until the track record is met; **the AI unsure, low confidence, timeout or AI off → admins, nothing deleted by default**; **no code path writes normal-message text to the corpus, the evidence store after an overturn, or any table outside the 72 h inbox**; **ask-admins mode never auto-bans** (neither variant, nor via timeout fallback or incoming network bans); **a policy violation alone never bans by default**; **established members are never auto-banned and always go to review**; **possibly hacked accounts are never auto-banned**; spam ≥ `T_delete` is deleted whatever the sender's trust (no trust bonus); a matrix cell `delete_only` never bans and `allow` never deletes spam; `auto_ban` with `require_source` never bans on other evidence sources; thresholds are monotonic; the ladder is inert unless enabled. |
| **Silent mode** | With `silent: true`, no code path sends a message to a protected group (checked at the action-queue level against the protected-group list); in-group commands are acted on, deleted, and answered by DM; onboarding DMs are only sent to admins with an open thread (except the documented one-per-day command reply). |
| **Spam-only evaluation & replay** | Two measurements, because there's no ham set. **Recall:** a replay harness runs the **spam-only hold-out slice** of the imported dataset (§7.8; split by cluster/campaign, so near-duplicates can't leak) through the full pipeline, reporting the share caught per band (CERTAIN / AI-verified / sent to admins / missed), per type and per preset, with diffs between rule versions. CI blocks a change that lowers recall. **Precision and false positives:** measured live from **shadow mode** and **admin review decisions**. Every action (or would-be action in shadow mode) that an admin overturns counts as a false positive, reported per band, source, type and group. Rule packs carry synthetic must-not-match examples (§22). The hold-out is versioned and never used for learning. |
| **Review flow** | Table-driven tests with a fake WA client: reaction/reply parsing (English + Arabic keywords, removed reactions), non-admin and late votes rejected, first-wins under concurrent votes (parallel goroutines against Postgres), edit-within-20-min vs. follow-up, timeout fallbacks, reviewer opt-in gating, fallback chain when the review group is gone. |
| **Identity** | LID-only, PN-only and both-known people; late PN resolution merges records and back-fills history; a pending watch rule fires after resolution; self-vCard linking only when the LID matches; recycled numbers close the old link and don't carry bans. |
| **Network bans** | Quorum branches (a/b/c) each tested, including the negative cases: a single admin with a medium score stays local; an established member or an admin elsewhere goes to the operator; `other` bans never leave the group. Scope `off`/`org`/`all`; LID-only, PN-only and both-known matching; "identity pending" resolved on a later join; admin re-add override; 40-group cleanup within pacing limits with batched kicks; the re-scan deletes only spam-matching messages (same fingerprint, same campaign, or ≥ T_delete) and keeps normal ones; too-old deletions skipped; unban reverses everything. |
| **Compromised accounts** | Property test: a known-spam match from a member at maximum trust is always deleted; a compromised event always creates an admin review and never leads to an automatic ban, strike, spammer profile or network ban; optional restriction (when enabled) deletes links/media/forwards in all groups and lets plain text through; `.clear`, `clear` reply and website clearing; admins only get an alert. Scenario tests for each trigger (cross-group burst, first links, campaign match, unusual hours, burst after silence). |
| **Spammer intelligence** | Property test: priors alone (including country code) never cause a delete or ban and never count as a signal family; lift computed against the group base rate; campaign linking on synthetic graphs; an upheld appeal deletes all of the person's intel rows. |
| **Learning** | Promotion only after ≥ 3 unrelated groups; overturn retracts rules and index entries and stores only a fingerprint allow-list entry (no text); the in-memory recent-traffic check rejects "good morning" marked as spam; per-admin rate limits; rollback by admin/group; replay-corpus gate on re-weighting. |
| **Status/broadcast** | Fixtures captured in the spike for status shares, replies quoting a status, status mentions, Group Status posts, and DM broadcasts. Asserts that the quoted/wrapped content reaches the envelope. |
| **Action queue** | Deterministic tests with a fake clock and a fake WA client: pacing, jitter bounds, priorities, coalescing, dedup, deadlines, warning signs → slow-down, with a **property test that the effective rate never exceeds the ceiling** and that recovery stops at the ceiling; kick batches never exceed `max_kick_batch`; every JID in a batch is individually eligible; the hourly kick cap defers rather than drops; breaker transitions, crash/restart recovery. |
| **WhatsApp interface** | All whatsmeow calls go through a narrow `WAClient` interface, so everything above runs without WhatsApp. Database-backed tests use **testcontainers-go** with a real Postgres. |
| **Staging** | A second number in a private test group with a few real phones. A scripted checklist for each release: every message type, delete/kick, edits, raid simulation, OTP login. whatsmeow updates often, so pin versions and run this checklist before bumping. |
| **Load** | A synthetic event generator at 10× expected peak to check CPU, memory, inbox lag and queue behaviour on ARM64. |
| **Dataset import** | JSONL and CSV parsing with required/optional fields and clear rejection reports; spam-only files import without warnings (`label` defaults to `spam`); `--dry-run` writes nothing; dedupe within the file and against the corpus; the hold-out split never puts two records with the same `cluster_id` (or the same computed near-duplicate cluster) on both sides; optional `ham` records are never written as text (only allow-list fingerprints if requested); stored text is redacted; `corpus remove --import <id>` removes every entry and fingerprint from that batch. |
| **Blacklist** | Number normalization (international, local with the group's country, `00` prefix, Arabic-Indic digits, `wa.me` links, several numbers per command, invalid numbers reported); PN→LID resolution batched and paced; a member on the list is banned silently, a group admin on the list is never acted on; join → kick, join request → reject, message → delete; the number in a vCard `waid`, a `wa.me` link or message text gives a `known` signal; instance entries become review items in ask-admins groups; remove lifts everything; every change is audit-logged. |
| **AI eval** | The imported dataset's hold-out slice (§7.8), plus later live samples: a fixed labeled set to compare models/prompts (accuracy, cost, latency) before changing the AI tier. |
| **Shadow in production** | New rules/weights can be deployed in *shadow* per rule, so we measure them on live traffic before they act. |

---

## 19. Deployment and self-hosting

Self-hosting is the main way to run the project, not an afterthought. The user's own instance on the namaa VM (Coolify) is one example of the generic path.

### 19.1 What a deployer gets

- **One `docker-compose.yml`** with two services: `bot` (all roles: connector, workers, web) and `postgres` (plain `postgres:16`). Optional profiles add `media` (OCR, video frames), `local-ai` (an Ollama container, for deployers who want models on their own server), and `monitoring` (Prometheus + Grafana).
- **`.env.example`**, with every variable documented: `DATABASE_URL`, `PUBLIC_URL`, `OPERATOR_PHONES`, `SESSION_SECRET`, optional AI settings (`AI_PROVIDER` = anthropic / openai_compatible / google, `AI_BASE_URL`, `AI_MODEL`, `AI_API_KEY`, `AI_TIMEOUT`; the AI tier stays off without them, and uncertain cases go to admins), optional vision settings in the same shape, `LOG_LEVEL`, retention overrides.
- **`config.yaml`** (optional) for defaults: the AI and vision providers (§7.4), presets, scoring weights, pacing parameters (ceiling, burst, slow-down/recovery, kick batch cap, daily alert level; raising the ceiling is marked as risky), rule-pack subscriptions, feed subscriptions, languages. Each setting has a comment and a safe default, and the bot starts with **no config file at all**.
- **A setup guide** (`docs/SELF_HOSTING.md`):
  1. requirements (a dedicated phone number, a small VM, 1 vCPU / 1 GB minimum, 2 vCPU recommended);
  2. `docker compose up -d`;
  3. open the website and log in as operator;
  4. pair the number (QR or 8-character pairing code shown on the website);
  5. add the bot to a group and make it admin.

  It also covers a reverse proxy (Caddy/Traefik/nginx examples, plus a Coolify example), backups, upgrades, the privacy note (§17.1) and the ban-risk advice (§3).
- **Images:** multi-arch (`linux/arm64`, `linux/amd64`) at `ghcr.io/rayanalyasi/antispam-bot`, built by GoReleaser in the release workflow (§23.10), signed (cosign keyless) and attested, with SBOMs. The tag scheme is in §23.4: pin `:0.1` or an exact version, never `latest`.

### 19.2 Build

- Multi-stage Dockerfile (stack in §4.1). Pure Go (`CGO_ENABLED=0`) with `jackc/pgx`, pure-Go image decoders (`image/jpeg`, `image/png`, `golang.org/x/image/webp` for static WebP) and `corona10/goimagehash`. The result is a ~25 MB distroless image.
- **`-media` variant:** animated WebP stickers (`x/image/webp` can't decode animation), video keyframes and OCR need `ffmpeg` / `libwebp` / `tesseract`. They ship as a second image tag (Debian slim) that runs the media workers (as subprocesses), so the core image stays minimal. Phases 1–2 can do without it by using thumbnails.

- **Frontend (from Phase 3):** a separate CI build of the frontend app produces static files that are copied into the image (served same-origin by the Go binary) or published as their own artifact.

### 19.3 Running it

- **Pairing:** the operator page on the website shows a QR or uses `PairPhone` to display a pairing code. Only operators can reach it (and it can be further restricted by IP or kept behind an SSH tunnel).
- **Reverse proxy:** only the website is exposed (HTTPS). Postgres is never exposed. For the user's namaa VM: a Coolify Docker Compose resource, with Traefik labels for the dashboard host and automatic TLS.
- **Backups:** a documented nightly `pg_dump` (any object storage; on namaa, Oracle Object Storage free tier). Losing the whatsmeow store means re-pairing; losing app data means losing settings and labels.
- **Upgrades:** migrations run automatically on start (forward-only), and release notes lead with breaking changes and upgrade steps (§23.7). A quick stop/start is fine: messages received during a restart are delivered on reconnect (`OfflineSyncPreview`/`OfflineSyncCompleted`) and processed afterwards. Because WhatsApp changes, deployers are told to keep instances updated (whatsmeow moves fast).

---

## 20. Phased roadmap

Durations assume one developer and are rough. **Releases come first:** v0.1 (below) is a small, stable slice for real use soon. The phases after it are the full plan, now labelled with the release they're expected to land in (tentative). Network bans and spammer intelligence stay in Phase 5. **The full scope stays:** network bans, spammer intelligence, AI verification and learning are all planned. Phases only order the work. Evidence and identity capture start in Phase 1, so the later phases have history to learn from.

### v0.1: the first release (comes first)

A **quick, stable first release** for a friend who needs protection soon. It's a deliberately small, well-tested slice of the design, aimed at real use in a few groups.

| v0.1 contains | Design reference |
|---|---|
| Intake and the Content Envelope for **all** message types (recursive unwrapping, edits, status wrappers) | §5, §6 |
| Text normalization (homoglyphs, invisible characters, Arabic, numbers) | §7.2 |
| **Known-spam matching:** words and phrases, links and domains, phone numbers, SimHash/MinHash near-duplicates, media and sticker hashes | §7.0 (CERTAIN band), §7.1 T0/T1 |
| The **`.blacklist`** command | §9.8 |
| Per-group content rules, delete-only mode, and the per-type **auto-ban matrix** with presets | §9.2, §9.3, §12 |
| **Silent** delete + ban (kick plus rejoin block) | §9.1, §9.7 |
| **Ask-admins mode** and the **review group** with ✅/❌ reactions and reply keywords | §8.3, §9.5 |
| Admin chat commands | §13.7 |
| The paced action queue (fixed ceiling, slow-down only, capped kick batches) | §10 |
| Shadow mode | §8.4 |
| **The AI check:** the provider-neutral `AIClassifier` with **OpenAI-compatible** and **Anthropic** adapters, the context bundle, uncertain band only, cache and cluster reuse, budgets, and the early-AI safety rule | §7.0, §7.4, §9.2 |
| The Postgres store and migrations, config, logging | §4.1, §14, §16 |
| Docker plus `docker compose up`, and the release pipeline | §19, §23 |

**Where "known spam" comes from in v0.1.** The full learning system arrives in v0.2. Until then, known spam comes from:
- rule packs and admin-added lists (words, phrases, domains, numbers);
- the `.blacklist`;
- the **exact and near-duplicate fingerprints** of messages that admins confirm as spam (`.spam`, review ✅), stored as plain fingerprints;
- AI few-shot examples drawn from those confirmed messages and the rule packs' examples.

Poisoning guards, promotion and re-weighting come with learning in v0.2.

**Not in v0.1 (v0.2 and later):**
- learning and dataset import;
- profile and link enrichment;
- hacked-account detection;
- the website and OTP login;
- vision AI;
- network bans and spammer intelligence;
- multiple bot numbers.

**Estimate:** ≈6–8 weeks for one developer, including the Phase 0 spike. It will be re-estimated after the spike.

**Exit criteria for `v0.1.0`:**
- `make check` is green, including the property tests:
  - only CERTAIN evidence auto-bans;
  - AI-only evidence never auto-bans;
  - ask-admins mode never auto-bans;
  - silent mode never posts in a protected group.
- **≥ 1 week of shadow mode** on the friend's and your own groups, with deletion precision ≥ 99% judged by admin reviews.
- At least one release candidate (`v0.1.0-rc.N`) tested by the friend.
- The release pipeline produces signed, attested multi-arch images (§23).

### The full plan, by phase (and the release each one is expected to land in)

| Phase | Scope | Exit criteria |
|---|---|---|
| **0. Spike** (≈1 week; part of v0.1) | Pair a test number with whatsmeow + Postgres store. Log every event type from real groups to a fixture corpus. Verify: admin revoke of others' messages, batched remove, edit/revoke events, view-once behaviour, vCard business fields, `GetNewsletterInfo*`, `GetGroupInfoFromLink`, the business profile description via raw IQ, LID/PN mapping in your groups, join-request node shape. **Also:** status shares/replies/mentions and Group Status posts as they arrive in groups (wrappers, `ForwardOrigin`, attributions), whether Group Status posts can be revoked, whether `status@broadcast` arrives from members who saved the bot, a broadcast list sent to the bot, reactions and replies on the bot's own messages in DMs and groups, and editing the bot's own message within 20 minutes. Measure safe pacing empirically (the default ceiling is set from this). **Batch-removal test:** remove dummy test accounts from a test group in batches of 1, 5, 8, 10 and 20, and record errors, ack latency, warnings and any account signals, to set `max_kick_batch`. | A written capability report; a fixtures folder; any library gaps confirmed with workarounds. |
| **1. Core + AI + learning** (≈8–10 weeks, in three milestones; **v0.1** ships the subset listed above, and the rest, mainly learning and dataset import, lands in **v0.2**) | **1a. Core and enforcement (≈4–5 weeks):** Connector, inbox, recursive unwrapper (including status/group-status wrappers), envelope for all types, T0 + T1 text (normalization, lists, fuzzy), URL canonicalization + domain lists, exact media/sticker hashes, **enforcement model**: routing (§8.3), per-type auto-ban matrix with evidence sources (§9.2), presets Relaxed/Standard/Strict/Ask-admins, always-ask-admins modes (a) and (b), ban = kick + rejoin block, policy violations as silent delete-only (§9.3), **silent mode** with inbound-first DM onboarding and command handling (§9.7, §13.6–13.7), the invisible "just joined" signal, policy blocks per type (including status shares/posts), action queue (complete: unbounded Postgres queue, priorities, fixed-ceiling slow-down-only pacing, kick batch cap, newest-first and deadline-aware ordering, batched kicks, backoff, breaker), members/trust as a routing input, **known spam always deleted regardless of trust** (§8.7), early lockdown on deletion backlog. **Command framework** with dotted-key `.set`/`.get` and the moderation commands (`.spam`, `.notspam`, `.ban spam\|other`, `.status`, `.mode`, `.preset`). **Reviews v1:** review group destination, reactions + reply keywords, admin-only first-wins, outcome edit/follow-up, timeout fallback. **Evidence capture** for confirmed spam starts now. **Identity and membership capture** also starts now: LID↔PN identities, join method / added-by / join-to-first-message time for every member, and name/picture history for spammers, so the network and intelligence features have history to work with later. **Local ban** with rejoin block. YAML presets. One bot number. **JSON API `/api/v1` with an OpenAPI spec** (the same settings layer the commands use; the frontend builds on it later). **Open-source scaffolding:** `docker-compose.yml`, `.env.example`, analyzer registry, rule-pack file format with tests, `locales/` (ar/en), CONTRIBUTING/SECURITY, CI with replay tests, multi-arch images.  **1b. AI and learning (≈3–4 weeks):** text AI tier T3 (AI gateway, structured output, budgets, cache, prompt caching, few-shot from the corpus), learning from confirmed spam (§7.5): group-local auto-fingerprints, SimHash/MinHash near-duplicate matching against the corpus, few-shot example selection by shared fingerprints/keywords/campaign, overturn retraction, poisoning guards (per-admin limits, admin reputation, in-memory recent-traffic check, rollback), and the **early-AI safety rule** (`ai` → ask admins until it has a track record, §9.2). **1c. Dataset import and evaluation (≈1 week):** the generic `antispam corpus import` command (JSONL/CSV, §7.8) with dedupe, cluster-aware hold-out split and undo; importing the operator's prepared spam dataset (spam-only by default); the hold-out slice as the first evaluation corpus, and a model comparison on it. **Manual phone-number blacklist** by command (§9.8). | **Before enforcing anywhere:** recall on the spam-only hold-out slice is reported per band and type; AI-only evidence and looser near-duplicate matches never auto-ban, and only CERTAIN evidence auto-bans (property tests). Then **≥ 1 week of shadow mode** in your own groups with the review queue on. **Precision comes from here:** admins review what the bot would have done, and enforcement starts only when shadow-mode precision for deletions is ≥ 99% (target to confirm) with no would-be ban of an established member. Also: zero automatic bans of admins/established members; the queue never exceeds pacing limits; a review can be opened and decided entirely in WhatsApp. |
| **2. Detection depth** (≈2–3 weeks; **v0.2**: profile and link enrichment, hacked-account detection, the optional ladder) | Thumbnail pHash, profile scanning (name/about/picture/business, plus own status when available), invite/channel/WA-link resolution, status attribution URLs and reshared channels, shortener expansion + reputation feeds + RDAP, SimHash campaigns across groups, behaviour (flood, join-and-post, cross-group), raid mode, the optional ladder (warn/mute, off by default), join screening/approval, edits re-analysis, view-once policy. **Compromised accounts** (§8.7): behaviour baselines, the possibly-compromised signal family, the "may be hacked" admin review, optional restriction while pending, `.clear`. **Reviews v2:** DM reviewers with opt-in, fallback chain, quiet hours, digests, confirm-before-kick. | Replay corpus precision ≥ 99% at the delete threshold (target to agree on); recall measured per type. |
| **3. Website & onboarding** (≈2–3 weeks; **v0.3**: the website, OTP login) | The separate frontend app (framework chosen then, §13.1) on top of the `/api/v1` API, served same-origin by the Go binary; OTP login (both methods), permissions, groups, settings with presets and previews, **reviews & notifications settings page, review queue, review history, learned-patterns page, blacklist page (§9.8)**, lists with dry-run, activity log, onboarding messages (including choosing a review destination), Arabic/English. Every website setting is checked against the `.set` command path for parity. **Self-hosting guide** (`docs/SELF_HOSTING.md`), operator setup flow with pairing on the website, first public release. | A non-technical admin can onboard a group, pick reviewers and change settings from a phone without help. |
| **4. Vision AI & learning at scale** (≈2–3 weeks; **v0.4**) | Vision AI tier (T4) for images/stickers/profile pictures, weekly bounded re-weighting gated by the replay corpus, global promotion of learned patterns (≥ 3 unrelated groups) with operator approval, track-record tracking and the opt-in to let `ai` auto-ban where it has earned it, appeals end to end. | Vision used only on the ambiguous few percent; re-weighting never lowers hold-out precision; overturn rate dashboard; promotion and unlock flows tested. |
| **5. Network bans & spammer intelligence** (≈3 weeks; **v0.5**) | Organisations and scope settings (`off`/`org`/`all`), quorum rule with operator approval queue, network-ban jobs (identity resolution incl. "identity pending" watches, batched kicks, re-scan of recorded messages that deletes only spam-matching ones within WhatsApp's delete window, rejoin kick, join-request rejection, admin re-add override), network-wide unban/appeal, audit. Spammer intelligence: profiles, snapshots, campaigns graph, number-prefix/picture/name/timing patterns as capped priors, website views (group and operator), privacy controls (retention jobs, access log, encryption, appeal deletion). | A test ban across ≥ 20 groups completes within pacing limits; every negative quorum case stays local; an unban fully reverses the ban; priors never act alone (property test in CI); privacy notice published. |
| **6. Scale & polish** (optional for most deployments; **later**: multi-number, multi-node) | Optional cross-instance feeds (rule packs, hashed fingerprints, §15); multiple bot numbers (optional, never required) with primary/standby, failover, leases, the `-media` image (animated stickers, video keyframes, OCR), optional voice transcription, multi-node, partitioning. | Survives a temp ban of one number without losing protection in groups that have a standby. |

---

## 21. Open questions and trade-offs

### Decisions made

- **v0.1 first:** a quick, stable first release for a friend (known-spam matching, `.blacklist`, content rules and the auto-ban matrix, silent delete + ban, ask-admins and the review group, commands, the paced queue, shadow mode, the provider-neutral AI check with OpenAI-compatible and Anthropic adapters, Postgres, Docker and the release pipeline). Learning, import, enrichment, hacked accounts, the website, vision, network bans and multi-number come in v0.2 and later (§20).
- **Releases and versioning** (§23):
  - **Versions:** SemVer, staying on 0.x until the operator surface is frozen (patches are safe; minors may break, with upgrade steps); no CalVer; tags are never moved.
  - **Release pipeline:** GoReleaser v2 runs in the release workflow after release-please has created the tag and a draft (`use_existing_draft`, publish last), with multi-arch images on GHCR, SBOMs, cosign keyless signing, attestations and Immutable Releases.
  - **Changelog and versions:** automated by release-please from Conventional Commits (`release-type: go`, `initial-version: 0.1.0`, `bump-minor-pre-major`). Its release PR into main holds only `CHANGELOG.md` and the manifest, and merging it replaces manual tagging. Visible `docs` commits trigger patch releases.
  - **Workflow and tokens:** one workflow on push to main with three jobs (`rp`, `goreleaser`, `back-merge`). A dedicated GitHub App, held in a `release` Environment, is the only bypass actor on the `v*` tag ruleset. An explicit tag-exists check guards against release-please's silent 422.
  - **Back-merge:** a throwaway `sync/main-<tag>` branch PR into staging, merged with a merge commit.
  - **PR titles:** `action-semantic-pull-request` on PRs into staging (Conventional types, lowercase subject).
  - **No docs agent:** docs are updated in the same PR as the change, and the judge enforces it (§23.11).
  - **Actions:** all pinned by SHA (table in §23.9).
  - **Docker tags:** `X.Y.Z` is immutable; `X.Y`/`latest` move on stable releases only; `edge` builds from staging.
  - **No LTS.**
  - **Where releases are cut:** release PRs from staging into main, then release-please's release PR, both merged with merge commits (release PRs are the one exception to "PRs into main come from staging"); `release/X.Y` branches only when needed.
  - **Upgrade safety:** forward-only migrations, and the binary refuses a newer schema.
  - **whatsmeow:** pinned pseudo-version with a weekly bump PR.
- **Open-source, self-hosted project** for big groups and communities. "Operator" = whoever deploys an instance, and network bans, the spammer database and learning are scoped to one deployment. Cross-instance sharing is opt-in feeds only (§15, §19, §22).
- **Full scope stays:** network bans, spammer intelligence, AI verification, learning (§20).
- **One bot number runs everything.** Multiple numbers are optional (Phase 6).
- **The queue has no maximum length.** Pacing stays: a fixed, conservative ceiling per bot that never ramps up automatically. It only slows down on warning signs, and raising it is a manual, risky operator choice (§10.3).
- **Kick batches are capped** (default 8 per call, to be validated in the spike), and batching never lowers the bar for kicking anyone (§10.4).
- **Default enforcement = instant delete + ban** (ban = kick + rejoin block, plus network rules for confirmed spam). The warn → mute ladder is optional and off by default (§9.1–9.3).
- **No trust grace on detection.** The same thresholds apply to everyone, and spam is deleted immediately whoever sent it. Trust and tenure only decide *who decides the ban*: new/unknown + confident spam → auto-ban (per the matrix); established member → admins decide (§7.3, §8.1, §8.3).
- **Hacked accounts:** delete, then admins decide the ban via a "may be hacked" review. Restriction while pending is optional. There's no automatic ban, spammer profile or network ban unless an admin decides (§8.7).
- **Silent bot by default.** Nothing is posted in protected groups. Onboarding is inbound-first by DM or on the website; in-group commands are acted on, deleted and answered by DM (§9.7).
- **New members:** an invisible "just joined" scoring signal, on by default. Visible content restrictions for new members are opt-in (§8.2).
- **Always-ask-admins mode** per group: (a) delete now and ask about the ban (recommended), or (b) ask before doing anything, with a timeout fallback that never bans (§8.3).
- **Per-type auto-ban matrix** (auto-ban / ask / delete only / allow, optionally by evidence source), filled by the Relaxed / Standard / Strict / Ask-admins presets, as a grid on the website and via `.autoban` (§9.2).
- **Spam vs. rule-breaking:** policy violations are deleted silently with no ban by default. Auto-ban is only for detected spam (§9.3).
- **Status and broadcast content is in scope**, including the sender's own status as a best-effort signal (§2.1, §6.18–6.20).
- **Reviews:** per group, DM reviewers (opt-in) or a review group. Admins decide by reaction or reply, the first valid admin wins, there's a timeout fallback, and everything is paced (§9.5).
- **Website and commands are both first-class**, and every setting can be reached by command (§13.0).
- **Learning:** only from confirmed spam, group-local until confirmed in 3 unrelated groups, with poisoning guards, and overturns retract (§7.5).
- **Network bans:** spam bans only, opt-in per group with scope, a quorum rule, network-wide undo. The cleanup deletes only spam-matching messages (§9.6).
- **Identity:** the LID is always stored; the phone number is resolved whenever possible and back-filled; bans match on either (§7.7).
- **AI and learning are in the first version** (Phase 1): text AI (T3), the learned spam corpus with fingerprint and SimHash/MinHash matching, few-shot examples, overturn retraction and poisoning guards. Vision AI, re-weighting and global promotion come in Phase 4. Network bans and spammer intelligence stay in Phase 5 (§20).
- **Early-AI safety:** AI-only evidence (and looser near-duplicate matches, which only feed the AI) routes to "ask admins", never auto-ban, until that source has a track record on the instance. Admin decisions on those reviews feed learning (§9.2).
- **Bootstrap data:** spam-only. The deployer imports a prepared spam dataset with the generic `antispam corpus import` command (JSONL/CSV, cluster-aware hold-out, §7.8). Ham is optional and never stored as text. Preparing the data is a private, one-off job outside the bot. *The earlier `whatsapp_logs.db` question is withdrawn.*
- **No collection of normal messages:** no ham corpus, no normal-message few-shot examples, no training on normal chat. Overturns store only a fingerprint allow-list entry. Normal messages exist only as the 72 h inbox and 7-day text-free fingerprints (§17).
- **No embeddings in the core:** no embedding model, no similarity tier, no pgvector. Reworded spam is caught by exact fingerprints + SimHash/MinHash, and anything looser goes to the AI. Few-shot examples are chosen by shared fingerprints/keywords/campaign. An optional `Embedder` interface is documented for the future only (§7.4, §4.1).
- **PostgreSQL only.** No SQLite lite mode (§4.1).
- **Stack:** Go modular monolith (roles: connector / workers / api); River for background jobs; custom per-number WhatsApp action executor; pgx + sqlc, goose, otter, zerolog + Prometheus; no external broker (§4.1).
- **Frontend decided later:** a separate frontend app over the versioned JSON API (`/api/v1`, OpenAPI), optionally served by the Go binary (§13.1).
- **Certainty routing is the core flow** (§7.0): CERTAIN acts per the matrix; UNCERTAIN goes to the AI with spam-only few-shot; still uncertain goes to admins. The "certain" thresholds are tight because there's no ham to calibrate against. Precision is measured from admin reviews and shadow mode; recall from the spam-only hold-out.
- **Manual phone-number blacklist in the first version**, by command (`.blacklist add/remove/list/check`), per group with instance-wide entries for the operator. It bans members on the list, keeps them out, and treats the number in content as known spam (§9.8).

### Still open

**Defaults for new groups** (the project ships these, and each deployment can change them):
1. **Start mode:** shadow for 24 h (safer, but feels like "it doesn't work"), or enforce (delete + ban per the Standard matrix) immediately?
2. **Review destination and timeout:** website-only until an admin picks one (current default), or DM the admin who promoted the bot when they have an open thread? Is "keep deleted, no ban" the right timeout fallback for ban decisions, and 15 minutes → delete for ask-admins mode (b)?
3. **"Certain" thresholds and the Standard matrix:** are the tight CERTAIN thresholds right (SimHash ≤ 3 bits, MinHash Jaccard ≥ 0.9, pHash ≤ 4), and the AI confidence cut-off of 0.9? Are the Standard preset's cells (§9.2) right, especially audio, polls and locations going to `ask`?
4. **The one exception to inbound-first:** is a single DM reply (max one per admin per day) to an admin who sent a command in the group, but never messaged the bot, acceptable? Or should the bot stay silent until they DM it?
5. **Network ban scope:** "My organisation only" (recommended) or "All groups of this deployment"? Contribute bans by default?
6. **Number-prefix signal:** on (weak and capped) or off until the operator reviews the patterns?
7. **Manual removals in the WhatsApp app:** ask "was it spam?" in the review destination, or ignore them?

**Thresholds:**
8. **Quorum and promotion:** keep "confirmed + (score ≥ 0.97 with 2 families, or 2 unrelated groups, or operator)" for network bans and "3 unrelated groups" for learned-pattern promotion? Should every network ban need operator approval during an instance's first weeks?
9. **Admin re-adds a network-banned person:** local exception (recommended) or kick and ask the operator?

**Privacy:**
10. **Profile photos:** hashes only (recommended) or also small encrypted thumbnails for reviewers?
11. **Retention:** corpus text 180 d, fingerprints 180 d after the last hit, spammer profiles while banned + 12 months. OK as defaults, and can groups set shorter limits?

**Technology:**
12. **Login method default:** "tap to message the bot" (recommended, ban-safe) or the bot sends an OTP?
13. **AI provider and model:** which provider/model should the project's **example config** use, and which will **your instance** use: a hosted model (more accurate, but message text and context leave the server) or a local model via an OpenAI-compatible server (data stays local; accuracy varies, especially for Arabic; slow on a CPU-only VM)? The eval harness (§18) can compare candidates on the spam hold-out and in shadow mode before you choose. And should AI verification be on by default for new groups on instances that have AI configured?
14. **AI context default:** send the context bundle (replies, the previous 5 messages, group info, sender context) by default (current), or default it off and let groups opt in? It improves accuracy but sends other members' recent messages to the AI provider.
15. **Track-record bar:** is "≥ 200 admin decisions with ≥ 98% confirmed as spam over 30 days, plus hold-out precision" the right bar before `ai` may be allowed to auto-ban?
16. **v0.1 AI provider for the friend's instance:** which OpenAI-compatible or Anthropic model will the friend's instance use for the v0.1 AI check, and what budget? It's the same question as Q13, but it needs an answer before the first rc.
17. **Release setup checks (§23.13):** pre-release tag behaviour, editable notes on immutable releases, `docker_signs` with `dockers_v2`, the App bypass and 422 on `createRef`, `require_extra_approval_for_unattributed_changes`, and release-please on `release/*` and for `-rc.N`. These are verification items, not open decisions. **Maintainer prerequisites** (the release App, the `release` environment, `ANTHROPIC_API_KEY`, and the "Actions may create PRs" setting vs. a docs App) are listed in §23.12.
18. **Website frontend framework:** React, Svelte or Next.js for the separate frontend app (§13.1)? To be decided when the full version is built; the API (`/api/v1`, OpenAPI) comes first either way.

**Project:**
19. **License:** keep MIT, switch to Apache-2.0, or use AGPL-3.0 so hosted forks stay open (§22.1)?
20. **Official feeds:** should the project itself publish a public rule-pack / fingerprint feed, and who maintains it?
21. **Languages** beyond Arabic and English for the first release?

**Your own instance (namaa):**
22. **Domain** for the website (Coolify's own panel stays behind the SSH tunnel), and the **bot number** to use (ideally an aged SIM, plus a spare).
23. **Jurisdiction:** where your instance operates (e.g. Saudi PDPL), and whether you want a legal review before enabling network bans and the spammer database for groups you don't run.

### Trade-offs made (and alternatives)

| Decision | Chosen | Alternative | Why |
|---|---|---|---|
| Queue/broker | Postgres: River for jobs, a custom paced executor for WhatsApp actions; unbounded | NATS / Kafka / RabbitMQ / Redis Streams | One dependency on a small VM, easy for self-hosters, and transactional enqueue avoids dual-write bugs. |
| Architecture | Modular monolith with roles | Microservices | The bottleneck is WhatsApp's write pace, not compute; the connector can't be split; fewer moving parts for self-hosters (§4.1). |
| Write pacing | Fixed conservative ceiling, slow-down only, plus early lockdown and capped batching | A rate that ramps up while things succeed; or no pacing | Success isn't proof WhatsApp is fine with a rate (bans can come without warning), so the rate never climbs on its own. Big waves are handled by lockdown, not speed. |
| Hacked accounts | Delete + admins decide the ban (restriction optional) | Treat like any spammer; or trust them | Hijacked members are real people, but their spam must still go. |
| Enforcement default | Instant delete + ban for confident spam from new senders; admins decide for established members | Warn → mute → kick ladder | Spammers ignore warnings; real members shouldn't be banned by a machine. The ladder stays optional. |
| Trust | Routing only (who decides the ban) | A trust bonus that lowers scores | A trust bonus let hijacked long-time accounts spam; same thresholds for everyone closes that gap. |
| Bot visibility | Silent in groups; admins use DM, the review group and the website | Welcome/warning/notice messages in groups | Less noise for members, and spammers learn less about how detection works. The bot is still visible as an admin. |
| Scoring | Weighted log-odds + calibration | Train an ML model | Explainable, debuggable, works with little data. ML can be added as one more signal. |
| Speed vs. certainty | Fast delete on T0/T1; slow lane can escalate | Wait for enrichment before any action | Spam visibility time matters more than delete precision at the margin. Kicks still wait for strong evidence. |
| Mute (optional ladder only) | Emulated via deletes | None (WhatsApp has no per-user mute) | It's the only option. Bounded by auto-escalation to a ban. |
| AI usage | Only for the UNCERTAIN band, one call per cluster, budgets, signal-not-verdict | AI on every message | Cost, latency, privacy, and prompt-injection risk. |
| AI provider | Provider-neutral interface: Anthropic, OpenAI-compatible (incl. local servers), Google; configurable | One vendor's SDK baked in | It's open source: deployers choose hosted vs. local, cost vs. accuracy, and compare them with the eval harness. |
| Media | Thumbnails first, download only when needed | Always download | Bandwidth/CPU/latency. Thumbnails catch most repeats. |
| Image pipeline | Pure Go core, optional `-media` image with cgo tools | cgo everywhere | Easy ARM64 multi-arch builds and a small attack surface. |
| Identity key | LID (fall back to PN) | Phone number | WhatsApp is moving groups to LIDs, and the PN isn't always visible. |
| Business profile description | Raw IQ / upstream PR | Ignore the field | It's a known spam surface. Small cost. |
| Notifications & reviews | Per group: chosen reviewers by DM (after opt-in) or a review group; reactions/replies to decide | Interactive buttons; email; website only | Admins already live in WhatsApp. Reactions and replies work on every client, but buttons from unofficial clients often don't render. Opt-in keeps DMs solicited. |
| Network bans | Opt-in, organisation scope by default, quorum (strong evidence, or 2 unrelated groups, or operator); admins can locally override | Any admin's ban applies everywhere | One admin must not be able to evict someone from unrelated communities. |
| Spammer intelligence | Capped "identity prior" signals that never act alone; hashes instead of photos | Use identity traits (e.g. country code) as real classifiers | Avoids false positives and discrimination by origin, and limits the personal data held. |
| Learning | Confirmed spam only, group-local first, promoted after ≥ 3 unrelated groups | Learn from every ban, globally | Admins ban for non-spam reasons, and global learning from one group invites poisoning. |
| Reworded spam | Exact fingerprints + SimHash/MinHash, then the AI | Embeddings + pgvector / a vector DB | Spammers mostly reuse templates (SimHash/MinHash catch near-copies); truly reworded spam goes to the AI, which understands meaning better; without normal-message data, embedding thresholds can't be calibrated. `Embedder` stays a documented future option. |
| Versioning | SemVer on 0.x, no LTS, tags immutable, `:0.1` as the stable channel | CalVer; an LTS line | One maintainer can't backport to parallel lines, and WhatsApp protocol breaks force upgrades anyway (§23.5). |
| Control surfaces | Website **and** commands over one settings model | Website-only or commands-only | Setup and history fit a website; daily moderation fits chat. Shared dotted keys keep them in sync. |

### Known limitations (to accept)

- View-once content can't be inspected (only blocked by policy or sender reputation).
- Deleted messages can't be restored on a false positive.
- "Silent" can't be total: the bot shows as an admin in the member list, deletions show as "deleted by admin", and bans and lockdowns produce WhatsApp's own system lines.
- WhatsApp may change protocol details. whatsmeow tracks them quickly, but breakage between WhatsApp changes and library releases is possible. Keep the library current and the staging checklist ready.
- Ban risk can be reduced, not eliminated.
- The sender's own Status is only visible when they share it with the bot's number. Broadcast lists are invisible to recipients (no list name, no other recipients), and a forwarded broadcast in a group looks like any other forward.
- Review messages can only be edited for 20 minutes after sending; later outcomes are posted as replies.
- Network cleanup only re-scans messages the bot recorded (content kept 72 h, fingerprints 30 d), deletes only the spam among them, and only inside WhatsApp's delete-for-everyone window (about 2 days). Older or unrecorded messages stay.
- Some people's phone numbers are never revealed (LID-only). Their records and bans work on the LID alone, but number-prefix patterns can't include them.
- A spammer with a new number, name and picture and no shared artifacts can't be linked to their old account. The intelligence database raises the cost of coming back; it doesn't make it impossible.

---

## 22. Open source: licensing and contributions

### 22.1 License

The repository is currently **MIT** (existing `LICENSE`). whatsmeow is **MPL-2.0**. That's file-level copyleft, so it works with MIT, Apache-2.0 or AGPL for our own code, as long as whatsmeow's own files stay MPL. The choice is still open (§21):
- **MIT/Apache-2.0:** maximum adoption.
- **AGPL-3.0:** anyone who runs a modified version as a service must publish their changes.

Either way:
- **Trademark/naming:** the project must not use WhatsApp/Meta branding beyond a factual "for WhatsApp".
- The README states plainly that this is an **unofficial client**, with ban risk (§3), and that each deployer is responsible for their own data and use (§17.1).

### 22.2 Contribution structure

- **Analyzers as plugins.**
  - Every analyzer implements the `Analyzer` interface (§6) and registers itself in a registry (`analyzers/<name>/`, compiled in, enabled/disabled in config).
  - Each analyzer ships with fixtures and table tests, and declares its tier, signal codes and default weights.
  - For non-Go contributors there's an optional **external analyzer** interface: an HTTP/JSON endpoint that receives a redacted envelope and returns signals within a timeout. It's off by default and treated as a T2-level signal.
- **Rule packs as files.**
  - `rules/packs/<name>.yaml` (keywords, phrases, regexes, domains, numbers, channel IDs, hashes), with metadata (languages, maintainer, version) and a `tests:` block of sample messages that must and must not match.
  - CI runs every pack against its own `tests:` block, including **synthetic must-not-match examples** written by contributors (no collected chat data), so a pack that hits normal phrasing can't be merged.
  - Instances subscribe to packs by name, and updates ship with releases or via a pack feed (§15).
- **Translations.**
  - All user-facing text (bot messages, command names and aliases, website, review keywords) lives in `locales/<lang>.yaml`. Arabic and English come first, and RTL is supported.
  - Bot message templates have several variants per language (to avoid identical-message patterns, §10.3).
  - A hosted translation platform (Weblate) is optional later.
- **Docs and process:**
  - `CONTRIBUTING.md` (dev setup with a fake WA client, so no WhatsApp account is needed to work on most of the code), `CODE_OF_CONDUCT.md`, and `SECURITY.md` (private vulnerability reporting).
  - Issue templates for new spam patterns: contributors attach *redacted* examples only.
  - **No real message data in the repo:** fixtures are synthetic or redacted with consent.
- **Governance (light):** maintainers review analyzers and global packs; CI runs replay and property tests (§18) on every PR; releases follow §23 (SemVer, 0.x policy, release PRs into `main`); a public roadmap mirrors §20.

---

## 23. Releases and versioning

How the project is versioned, built, released and upgraded. These are decisions from research and a verified automation setup. Brief sources are given inline, and the items still to check on the first release are at the end (§23.13).

### 23.1 Version numbers

- **SemVer `vX.Y.Z`** ([semver.org](https://semver.org)). **No CalVer.**
- **We stay on 0.x until the operator surface is frozen.** The *operator surface* is everything a deployer depends on:
  - environment variables and config keys;
  - chat commands;
  - the image contract (entrypoint, ports, volumes, user);
  - the database upgrade path.
- **0.x policy:**
  - a **patch** release (`0.1.N`) is **fixes only and always safe** to apply;
  - a **minor** release (`0.Y.0`) **may break things**, and its notes list the upgrade steps.
- **Pre-releases** look like `v0.1.0-rc.1` (how they're cut with release-please is in §23.12 and §23.13).
- **Tags are permanent.** A pushed tag is never moved or deleted: the Go module proxy and checksum database treat versions as immutable ([Go modules reference](https://go.dev/ref/mod)). A bad release is fixed with a **new version**, plus a `retract` directive in `go.mod` if needed.
- If the project ever reaches **v2**, the module path needs a **`/v2`** suffix (Go's major-version rule).

### 23.2 Release mechanics

- **GoReleaser v2** ([goreleaser.com](https://goreleaser.com)) builds and publishes each release. It runs as the `goreleaser` job of the release workflow, right after release-please has created the tag and a draft release (§23.10), not on a separate tag-push trigger.
- **Binaries:**
  - linux and darwin × amd64 and arm64, `CGO_ENABLED=0`, `-trimpath`;
  - `ldflags` embed the **version, commit and build date** (shown by `antispam version` and in logs and metrics).
- **Images:** multi-arch images built with GoReleaser's **`dockers_v2`**, pushed to **`ghcr.io/rayanalyasi/antispam-bot`** (GHCR requires lowercase names).
- **Supply-chain extras:**
  - **SBOMs** for archives and images;
  - **cosign keyless signing** (Sigstore) of the checksums file and the images;
  - **build provenance** via `actions/attest`;
  - **GitHub Immutable Releases** turned on, so published release assets and tags can't be altered ([GitHub docs: immutable releases](https://docs.github.com/en/code-security/supply-chain-security/understanding-your-software-supply-chain/immutable-releases)). Because a published release can't change, all assets are uploaded to a **draft** first and the release is published last (§23.10).

### 23.3 Changelog and version bumps: release-please

The repository uses **Conventional Commits** (switched in PR #4), so **[release-please](https://github.com/googleapis/release-please)** automates the version and the changelog. It replaces the hand-curated `CHANGELOG.md` and the label-based generated notes.

- **How it works:** `release-please-action` v5.0.0 runs on every **push to `main`**. It keeps a **release PR into `main`** open that contains **only** `CHANGELOG.md` and `.release-please-manifest.json`. Merging that PR creates the release (§23.10).
- **Configuration** (`release-please-config.json`):

  | Setting | Value | Why |
  |---|---|---|
  | `release-type` | `go` | Go project, no version file to bump |
  | `initial-version` | `0.1.0` | without it, a `0.0.0` manifest would ship `1.0.0` |
  | `bump-minor-pre-major` | `true` | on 0.x, a breaking change bumps the **minor** (matches the 0.x policy in §23.1) |
  | `bump-patch-for-minor-pre-major` | `false` | on 0.x, a `feat` bumps the minor (new behaviour isn't "fixes only") |
  | `include-component-in-tag` | `false` | tags are plain `vX.Y.Z` (GoReleaser matches the draft by that exact name) |
  | `bootstrap-sha` | the last pre-rewrite commit | keeps the legacy Node commits out of the first changelog |
  | `draft` / `force-tag-creation` | `true` / `true` | needed for Immutable Releases (§23.10) |

- **Changelog sections:**
  - **visible:** Features (`feat`), Bug Fixes (`fix`), Performance (`perf`), Documentation (`docs`), Reverts (`revert`);
  - **hidden:** `refactor`, `chore`, `test`, `ci`, `build`.

  **Visible `docs` commits trigger patch releases,** so let docs changes batch up on staging rather than releasing for each one.
- **Breaking changes and upgrade steps** are written in the commit's **`BREAKING CHANGE:` footer** (or as `feat!:`). release-please puts them at the top of the release notes under "⚠ BREAKING CHANGES", which gives the "Breaking changes / Upgrade steps first" rule in §23.7. The GitHub release body is release-please's notes; GoReleaser's own changelog is disabled.
- **Merging:** release-please's release PR, like the staging → main release PR, is merged with a **merge commit, never squashed**. Release PRs are the **one allowed exception** to "PRs into `main` come from `staging`".

### 23.4 Docker tags

| Tag | Meaning |
|---|---|
| `X.Y.Z` (e.g. `0.1.3`) | **immutable**, one per release |
| `X.Y` (e.g. `0.1`) | moves to the newest **stable** patch of that minor. This is the "stable channel". |
| `latest` | moves only on **stable** releases; not recommended for pinning |
| *(no bare `0`)* | not published while on 0.x, because 0.x minors may break |
| `edge`, `sha-<7>` | built from **every push to `staging`**, for testing only |
| `X.Y.Z-rc.N` | pre-releases get **only their exact tag**; they never move `X.Y` or `latest` |

Images carry the standard **OCI labels** (source, version, revision, created, licenses).

### 23.5 No LTS

There's no long-term-support line:
- **LTS means parallel branches and backports**, which one maintainer can't promise.
- **A WhatsApp protocol break forces everyone to upgrade anyway.** An "old but supported" version would stop connecting.

The moving **`0.1` tag is the stable channel.** The docs tell deployers to pin **`:0.1`** (automatic safe patches) or an **exact version**, and **never `latest`**.

### 23.6 Where releases are cut

- **The normal path:**
  1. A **release PR** from `staging` into `main` is merged **with a merge commit** (not a squash), so `main` contains the exact commits that were tested on staging.
  2. The push to `main` makes release-please open or update **its release PR** (`CHANGELOG.md` + manifest) into `main`.
  3. Merging that PR (merge commit) lets the release workflow create the tag `vX.Y.Z` on it and publish the release (§23.10).
  4. `main` is merged back into `staging` (§23.10).
- **`release/X.Y` branches** are created **only when needed:** when a patch must ship while `staging` holds unshippable work.
  - Fixes go **upstream first** (to `staging`).
  - They're then **cherry-picked by PR** into `release/X.Y`.
  - The release workflow also runs for `release/*`, with release-please targeting that branch. This is to verify (§23.13).
- **Repository settings:**
  - a **tag ruleset on `v*`** that restricts creating, updating and deleting tags, with the **release App as the only bypass actor** (§23.10);
  - `release/*` added to the **branch ruleset** (PR-only, like `main` and `staging`);
  - the release workflow runs only for pushes to `main` or `release/*`, so it never tags anything else.

### 23.7 Upgrade safety

- **Migrations are forward-only** (goose, embedded and run on start, §4.1).
- **The binary refuses to start on a schema newer than it knows**, with a clear message ("database is from a newer version; upgrade the bot or restore a backup").
- **Skipping versions within a major is supported** (e.g. 0.1.2 → 0.3.0 directly). Migrations run in order.
- **Patch releases must not contain migrations that block a downgrade within `X.Y`.** Going from 0.1.4 back to 0.1.3 must keep working.
- **What counts as breaking** (minor bump in 0.x, major after 1.0):
  - removing or renaming an env var, config key or command;
  - changing a default in a way that changes enforcement;
  - an image contract change (entrypoint, ports, volumes, user);
  - a migration that can't be rolled back within the minor;
  - dropping a supported platform;
  - **anything that forces WhatsApp re-pairing.**
- **Deprecations** produce a **warning for at least one minor release** before removal (logged on start, and shown on the operator page once the website exists).
- **Release notes lead with "Breaking changes / Upgrade steps"**, then the standard procedure:
  1. back up (`pg_dump`);
  2. `docker compose pull`;
  3. `docker compose up -d`.

### 23.8 Keeping up with whatsmeow

- **whatsmeow has no tags**, so we pin its **exact pseudo-version** in `go.mod`.
- A **weekly workflow** (or Renovate) opens a **bump PR into `staging`**. CI and the staging checklist run on it, and it ships with the next release.
- **The same-day patch path for protocol breaks:**
  1. WhatsApp starts rejecting the client ("405 client outdated", `events.ClientOutdated`);
  2. bump whatsmeow;
  3. release **`v0.1.N`** through the hotfix path (§23.12).

### 23.9 Supply chain

- **Actions are pinned by commit SHA**, not by tag (the repository requires it).
- **Dependabot** covers `gomod`, `github-actions`, `docker` and the compose file, with updates **grouped** to limit PR noise. It doesn't bump whatsmeow's pseudo-version, so that's left to the weekly workflow.
- **`SECURITY.md`:** private vulnerability reporting through GitHub, the supported versions (the latest minor), and the response expectations.

**Pinned actions** (as of 2026-10-17; Dependabot keeps them current):

| Action | Version | SHA |
|---|---|---|
| `googleapis/release-please-action` | v5.0.0 | `45996ed1f6d02564a971a2fa1b5860e934307cf7` |
| `amannn/action-semantic-pull-request` | v6.1.1 | `48f256284bd46cdaab1048c3721360e808335d50` |
| `goreleaser/goreleaser-action` | v7.3.0 | `a4f02ab5355a589de60d917a659ce617ed19b3de` |
| `actions/create-github-app-token` | v3.3.0 (use `client-id`) | `64ab729a6294000f5aa4a4bc0b73d1f32bc45510` |
| `actions/checkout` | v7.0.1 | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| `actions/setup-go` | v7.0.0 | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` |
| `docker/setup-qemu-action` | v4.4.0 | `99012661954931238ded8c8b007157a8430204e1` |
| `docker/setup-buildx-action` | v4.4.1 | `f87e5991a6d7451dcb8d9637bfbc97413f497069` |
| `docker/login-action` | v4.6.0 | `dbcb813823bdd20940b903addbd779551569679f` |
| `sigstore/cosign-installer` | v4.1.2 | `6f9f17788090df1f26f669e9d70d6ae9567deba6` |
| `actions/attest` | v4.2.2 | `1e69f48acb82d1966a394da916b4c1698aa569d6` |

### 23.10 The release workflow

#### Immutable Releases: draft first, publish last

With Immutable Releases on, a published release can't gain or change assets. So the release is created as a **draft**, filled, and published at the end:

1. **release-please** (`draft: true`, `force-tag-creation: true`, which needs release-please core 17.2+):
   - creates the **`vX.Y.Z` tag** itself through `POST /git/refs`;
   - then creates a **draft release** named `vX.Y.Z` with the changelog as its body.
2. **GoReleaser v2:**
   - `release.use_existing_draft: true` finds that draft by its name `vX.Y.Z` (so no component in the tag);
   - `mode: keep-existing` keeps release-please's notes;
   - `changelog.disable: true`;
   - `replace_existing_artifacts: true` lets a re-run replace partial uploads;
   - `release.draft: false` means that, after uploading everything, it **publishes** the release, which locks it.
3. **Silent-failure guard:** release-please **swallows a 422** on tag creation, so a tag blocked by the ruleset would fail silently and GoReleaser would find no tag. The workflow therefore has an explicit **"tag exists" check** step after release-please, and it fails loudly if the tag is missing.

#### One workflow, three jobs

Events caused by `GITHUB_TOKEN` don't start other workflows, so a tag created inside one workflow can't trigger a separate "on tag" workflow. Everything runs in **one workflow on `push` to `main`** (and `release/*`):

| Job | Runs when | Does |
|---|---|---|
| `rp` | every push | release-please: keeps its release PR up to date; when that PR was just merged, creates the tag and the draft release, and outputs `release_created` |
| `goreleaser` | `needs: rp`, only if `release_created` | the tag-exists check, then builds, signs, attests, uploads and publishes (above) |
| `back-merge` | after a release | merges `main` back into `staging` (below) |

#### Tokens: a dedicated GitHub App

- The release jobs use tokens from a **dedicated GitHub App** (via `actions/create-github-app-token`, with `client-id`):
  - permissions: **Contents, Pull requests and Issues: read and write**;
  - installed on **this repository only**.
- The App is the **only bypass actor on the `v*` tag ruleset**. `GITHUB_TOKEN` gets **no** bypass, because that would let any workflow on any branch create release tags.
- The App's credentials live in a GitHub **Environment `release`** restricted to `main` (and `release/*`), so only the release workflow on those branches can use them.
- Bonus: PRs and tags created with the App's token **do trigger workflows** (unlike `GITHUB_TOKEN`), so release-please's PR and the back-merge PR get normal CI.

#### Back-merge: main → staging

- After a release, `main` holds commits `staging` lacks (the release PR's `CHANGELOG.md` and manifest, and the merge commits).
- The `back-merge` job cuts a throwaway branch **`sync/main-<tag>`** from `main` and opens a PR into `staging`, merged with a **merge commit, never squashed**.
- It doesn't open a PR from `main` directly. A PR from `main` can't be updated or conflict-resolved without commits to `main` itself; the throwaway branch can be fixed by hand if a conflict appears.

### 23.11 PR title lint and docs upkeep

#### PR title lint

- **`amannn/action-semantic-pull-request`** v6.1.1 runs on **`pull_request_target`** for PRs into `staging`.
- Allowed types: `feat`, `fix`, `perf`, `docs`, `refactor`, `test`, `ci`, `build`, `chore`, `revert`.
- The subject **must not start with an uppercase letter**.
- PRs into staging are squash-merged, so the PR title becomes the Conventional Commit that release-please reads.

#### Docs upkeep (no docs agent)

There is **no automated docs agent** (decided 2026-10-17). User-facing docs stay current through the normal feature flow:
- the builder updates README and `docs/` in the **same PR** as any change to behaviour, configuration, commands or deployment;
- the judge treats a missing docs update as a **major** finding (CLAUDE.md, "Commits and pull requests").

This avoids an extra paid agent run on every merge, and the repository secrets and PR-creation setting it would need.

### 23.12 The maintainer's release procedure

**One-time prerequisites** (before the release features F-094 and F-097):
1. Create the **release GitHub App** (Contents, Pull requests and Issues: read and write) and install it on this repository only.
2. Add the App as the **only bypass actor** on the `v*` tag ruleset.
3. Create the **`release` Environment**, restricted to `main` (and `release/*`), holding the App's client ID and private key.

**Normal release:**
1. **Release PR** `staging` → `main`, merged with a **merge commit**.
2. release-please opens or updates **its release PR** into `main`. Review the generated `CHANGELOG.md`, especially the "⚠ BREAKING CHANGES" section, whose upgrade steps come from `BREAKING CHANGE:` footers.
3. **Merge release-please's PR** (merge commit). This replaces manual tagging. The workflow creates the tag and the draft, and GoReleaser uploads, signs, attests and publishes.
4. **Verify** the published release:
   - the image runs on amd64 and arm64;
   - `cosign verify` passes on the image and the checksums;
   - `gh attestation verify` passes;
   - the SBOM is present;
   - the `X.Y` and `latest` tags moved.
5. **Merge the back-merge PR** (`sync/main-<tag>` → `staging`, merge commit).

**Release candidates for the friend:** the friend tests the **`sha-<7>`/`edge` image** of the staging commit that's about to be released (shadow mode first for a minor release), *before* step 1. A tagged `vX.Y.Z-rc.N` would use release-please's prerelease versioning; that's to verify (§23.13).

**Hotfix path:**
1. Fix on `staging` first (upstream first).
2. If `staging` is shippable, do a normal release, as above.
3. If not:
   - create `release/X.Y` from the last release tag;
   - cherry-pick the fix by PR;
   - release-please's PR on `release/X.Y` produces `vX.Y.(Z+1)`;
   - verify, then back-merge.
4. For a WhatsApp protocol break (§23.8), the same path is the same-day route.

### 23.13 To verify on the first release

- How `docker/metadata-action`'s **`latest=auto`** (or GoReleaser's tag templates) behaves on pre-releases, so a pre-release never moves `latest` or `X.Y`.
- Whether release notes **stay editable** on **immutable releases**.
- Whether GoReleaser's **`docker_signs`** covers images built with **`dockers_v2`**.
- Whether the tag ruleset honours the **App bypass for `createRef`** (`POST /git/refs`).
- Whether a ruleset-blocked `createRef` really returns **422** (the case the "tag exists" check guards against).
- What **`require_extra_approval_for_unattributed_changes`** on the branch ruleset means for App-authored and back-merge PRs.
- release-please on **`release/*`** (target branch) for hotfixes, and its **prerelease versioning** (`-rc.N`) for tagged release candidates.

---

*Appendix: library facts in §2 come from reading `go.mau.fi/whatsmeow` at commit `0b0a1cd` (files `send.go`, `message.go`, `broadcast.go`, `group.go`, `user.go`, `newsletter.go`, `download.go`, `client.go`, `msgsecret.go`, `types/*.go`, `types/events/*.go`, `proto/waE2E`, `proto/waStatusAttributions`). Items marked "to confirm in spike" are protocol behaviour I couldn't verify from source alone.*
