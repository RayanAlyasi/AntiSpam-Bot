# Locales

Every user-facing text lives here, one file per language (`en.yaml`, `ar.yaml`). The files are
embedded in the binary and checked when it starts and in CI:

- every file has the same keys under `messages`, `commands` and `keywords` as `en.yaml`;
- every message has at least two variants, so the bot doesn't send the same text over and over;
- every variant of a message, in every language, uses the same `{placeholders}`;
- a command alias or reply keyword means one thing only, across all languages.

To add a language, copy `en.yaml` to `<code>.yaml`, set `lang` and `dir` (`ltr` or `rtl`),
translate the texts and add command aliases, then run `go test ./locales/`.
English command names always work; the aliases add to them.
