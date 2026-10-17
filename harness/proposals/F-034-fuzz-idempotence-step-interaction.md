# Proposal: idempotence fuzz must cover step interactions

Rule: When a pipeline step deletes or folds characters, fuzz `f(f(x)) == f(x)` with seeds where the deletion joins runs that an earlier step counted or limited, because the second pass sees a different run structure.

File: the builder checklist in docs/AGENTIC_ENVIRONMENT.md (or the text-processing rules file).
Incident: harness/incidents/F-034-judge-round1-majors.md
