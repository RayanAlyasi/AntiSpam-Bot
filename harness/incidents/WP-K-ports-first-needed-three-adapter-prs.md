# WP-K: shipping against ports before dependencies cost three follow-up adapter PRs

Symptom: WP-K merged before identity, corpus and store existed, then needed adapter and wiring PRs (including #39 wiring). Several judge majors were adapter mismatches (ErrNoPerson failing listings, ByPerson creating persons, zero Hasher, nil Norm).

Root cause: fakes accepted what real stores reject, so ports hid contract gaps until wiring.

Evidence: .agent/logs/judge-WP-K.md re-review 1.

Prevention: conformance tests on each port, run against both the fake and the real adapter.
