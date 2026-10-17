package blacklist

// MemoryList is an in-memory List, safe for concurrent use.
type MemoryList struct{ *memList }

var _ List = MemoryList{}

// NewMemoryList returns an empty MemoryList.
func NewMemoryList() *MemoryList { return &MemoryList{&memList{}} }
