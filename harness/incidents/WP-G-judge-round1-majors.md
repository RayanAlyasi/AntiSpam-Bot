# WP-G: judge round 1 FAIL with four majors

- Symptom: (1) fuzzy phrase matching had unbounded cost: a 60 KB message against 100 phrases took 3.97 s, holding the T0 tier; (2) corpus URLs kept wa.me phone numbers; (3) a weighted domain/URL/exact rule stopped the precedence walk and hid known evidence; (4) circled, superscript, fullwidth and math digits evaded number extraction.
- Root cause: (1) per-item cost reasoned about, not per-message total; (2) redaction applied to text but not to URLs inside corpus entries; (3) first-hit-wins walk treated weighted rules as decisive; (4) number extraction ran on raw text, not Norm.
- Evidence: .agent/logs/judge-WP-G.md; scratch proofs in .agent/scratch/WP-G/ (fuzzy_cost_test.go, corpus_url_leak_test.go, domain_mask_test.go, blacklist_digits_test.go); fixed in 8aa2f2f, round 2 PASS.
- Would have prevented: a worst-case cost test and per-message bound written with the first matcher (proposal F-024-parser-cost-test.md, second occurrence); a test per sensitive-data sink; property tests that a weighted hit never removes stronger evidence.
- Open minors left: skeleton matching per surface rather than per word (mixed Arabic/English false hard matches), concurrent overturn orphaning learned rules, keycap-emoji digits, same-scope weighted subdomain hiding a hard parent.
