# F-106: Go Anthropic SDK does not strip unsupported JSON-schema keywords

- Symptom: wp-e's schema used minimum, maxLength and uniqueItems, which the API path does not support.
- Root cause: the assumption that the SDK normalises schemas was wrong.
- Evidence: llm.PortableSchema added in the F-109 work (commit 1ab1c28, d4e46b8).
- Prevention: check `go doc` and the API's supported-schema docs before relying on keywords. Run all schemas through llm.PortableSchema.
