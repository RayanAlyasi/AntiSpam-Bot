# Deviations: contact-phone-shapes

- Contract "not a phone": runs whose groups are all 1–2 digits. Built: three or more 1–2-digit
  groups anywhere in the run (the judge's suggestion), and a lone `0` first group isn't a trunk
  prefix. Stricter, so "055 12 34 56" and "0 50 100 150 200" are not phones. Why: DET-002, a
  false phone plus related wording sends ordinary chat to admins.
