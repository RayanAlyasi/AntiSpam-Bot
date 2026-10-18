# holdout-recall: gosec G101 flagged the identifier stopWords

Symptom: lint failed with G101 (hardcoded credential) on `stopWords`.

Root cause: G101 matches identifiers case-insensitively for credential-like substrings, and "stopWords" contains "pW".

Evidence: .agent/logs/check-lint.log; renamed to noiseWords in c595b99.

Prevention: avoid credential-like substrings (pw, pass, secret, token) in names of string-valued variables.
