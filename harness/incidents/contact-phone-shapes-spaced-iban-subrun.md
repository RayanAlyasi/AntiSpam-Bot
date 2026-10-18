# contact-phone-shapes: sub-run search read spaced IBANs/cards as phones

- Symptom: judge round 1 FAIL (major). Spaced IBANs, cards and account numbers produced phone hits.
- Root cause: to find adjacent numbers, phoneIn split a long digit run at spaces and searched every 9-15-digit stretch. 0-led interior groups of an IBAN/card/account then looked like national phone numbers.
- Evidence: .agent/logs/judge-contact-phone-shapes.md (round 1 section), .agent/scratch/contact-phone-shapes/judge-round1.md.txt; fix in e8cf31c (DP over parts: each must be a phone, date/year or short count, at least one phone).
- Residual (round 2 minors): "SA 44 2000 0001 2345 6789 2012" and "حساب 0108 0952 3456 0789 1234 56" still read as phones.
- Prevention: when widening a detector to search sub-spans, first list the non-phone digit shapes (IBAN, card, account, dates) and add them as negative table tests before writing the search; require that the whole run is explained, not just a stretch inside it.
