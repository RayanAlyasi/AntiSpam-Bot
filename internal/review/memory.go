package review

// MemoryStore is an in-memory Store, safe for concurrent use. The Postgres store replaces it in
// production; tests use it.
type MemoryStore struct{ *memStore }

var _ Store = MemoryStore{}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{newMemStore()} }

// MemoryEvidence is an in-memory EvidenceStore, safe for concurrent use.
type MemoryEvidence struct{ *memEvidence }

var _ EvidenceStore = MemoryEvidence{}

// NewMemoryEvidence returns an empty MemoryEvidence.
func NewMemoryEvidence() *MemoryEvidence { return &MemoryEvidence{newMemEvidence()} }
