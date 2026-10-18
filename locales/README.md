# Locales

User-facing text in variants, command aliases and review keywords live here, one file per
language (`en.yaml`, `ar.yaml`). In v0.1 the texts the bot actually sends (replies, review cards,
help pages, onboarding) come from the built-in catalogue in `internal/review/catalog.go` and
`internal/review/catalog_cmd.go`, which is their only source; a message isn't kept in both
places. Moving that catalogue here is planned. The files are
embedded in the binary and checked when it starts and in CI:

- every file has the same keys under `messages`, `commands` and `keywords` as `en.yaml`;
- every message has at least two variants, so the bot doesn't send the same text over and over;
- every variant of a message, in every language, uses the same `{placeholders}`;
- a command alias or reply keyword means one thing only, across all languages.

To add a language, copy `en.yaml` to `<code>.yaml`, set `lang` and `dir` (`ltr` or `rtl`),
translate the texts and add command aliases, then run `go test ./locales/`.
English command names always work; the aliases add to them.
