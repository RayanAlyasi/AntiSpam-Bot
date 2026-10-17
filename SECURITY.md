# Security policy

## Reporting a vulnerability

Please report vulnerabilities **privately** through GitHub's private vulnerability reporting:
[open a draft advisory](https://github.com/RayanAlyasi/AntiSpam-Bot/security/advisories/new)
(the repository's **Security** tab → **Report a vulnerability**).

Don't open a public issue, pull request or discussion for a security problem.

A useful report says:
- which release or image tag you run;
- what an attacker can do, and what they need first (a group member, an admin, network access to the website, …);
- the steps to reproduce it, or a proof of concept.

Please don't include real WhatsApp messages, phone numbers or session files. Redact them or describe them.

## Supported versions

There's no long-term-support line ([versions and upgrading](docs/VERSIONS.md)).
Security fixes go into the **latest minor release** only, as a new patch release.

| Version | Supported |
|---|---|
| The latest `0.Y.x` (pin the `:0.Y` image tag) | ✅ |
| Older minors | ❌ Upgrade to the latest minor |
| `edge` and `sha-<7>` images | ❌ For testing only |

## What to expect

This is a volunteer project with one maintainer, so these are aims, not guarantees:
- an acknowledgement within **7 days**;
- an assessment (accepted or not, and how serious) within **14 days**;
- for accepted reports, a fix released as a patch release as soon as it's ready, then a published GitHub security advisory that credits you unless you'd rather stay anonymous.

Please give us a reasonable time to ship a fix before you disclose publicly. We'll agree a date with you.

## Verifying releases

Release images and archives are signed with cosign (keyless, Sigstore) and carry GitHub build-provenance attestations.
[docs/VERSIONS.md](docs/VERSIONS.md#verifying-a-release) shows how to check them.

## Scope

In scope: this repository's code, release images and archives, and the default configuration.

Out of scope:
- WhatsApp itself and the whatsmeow library: report those upstream;
- the account-ban risk of running an unofficial WhatsApp client, which is documented and inherent;
- problems that need an already-compromised server or database.
