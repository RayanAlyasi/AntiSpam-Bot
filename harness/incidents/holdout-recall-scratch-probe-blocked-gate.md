# holdout-recall: architect scratch probe under the hidden tests dir broke lint and the builder could not remove it

Symptom: the stop gate stayed red on a gofumpt lint failure until the architect deleted the folder.

Root cause: the architect left a scratch probe folder under the hidden tests dir. Lint covers it, and the guard stops the builder from editing that dir.

Evidence: .agent/logs/check-lint.log, harness/progress/holdout-recall.md.

Prevention: put throwaway probes only in .agent/scratch/ and delete any probe before handing off.
