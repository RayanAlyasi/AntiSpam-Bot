# Proposal: let agents write `.env.example`

`harness/scripts/hooklib.py` blocks every path starting with `.env` (`SENSITIVE` plus the
`p == ".env" and relpath.startswith(".env")` rule), and `.claude/settings.json` denies
`Read(./.env.*)`. DESIGN.md §19.1 and F-091 ask for a committed `.env.example`, which holds no
secrets, so F-091 shipped `env.example` instead.

Suggested change (human review, protected files):
- hooklib.py: treat `.env.example` as safe, e.g. `and relpath != ".env.example"` in the `.env` rule.
- settings.json: add `"Read(./.env.example)"` to `allow` (or narrow the deny to `./.env` and `./.env.local`).
- Then `git mv env.example .env.example` and update README.md, docker-compose.yml and
  cmd/antispam/deploy_test.go (it reads `env.example`).
