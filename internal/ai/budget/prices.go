package budget

import (
	"errors"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/llm"
)

// Prices are the provider's token prices in micros (millionths of a currency unit) per million
// tokens. Zero prices give zero cost, which suits local models; their cost shows as latency.
type Prices struct {
	InputPerMTok      int64
	OutputPerMTok     int64
	CacheReadPerMTok  int64
	CacheWritePerMTok int64
}

// Validate rejects negative prices.
func (p Prices) Validate() error {
	if p.InputPerMTok < 0 || p.OutputPerMTok < 0 || p.CacheReadPerMTok < 0 || p.CacheWritePerMTok < 0 {
		return errors.New("budget: prices must not be negative")
	}
	return nil
}

// Usage converts a provider's token usage into what ai_usage records. Input tokens include the
// cache reads and writes; cost rounds up so that many tiny calls never add up to zero.
func (p Prices) Usage(u llm.Usage) Usage {
	micros := u.InputTokens*p.InputPerMTok + u.OutputTokens*p.OutputPerMTok +
		u.CacheReadTokens*p.CacheReadPerMTok + u.CacheWriteTokens*p.CacheWritePerMTok
	return Usage{
		InputTokens:  u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens,
		OutputTokens: u.OutputTokens,
		CostMicros:   (micros + 999_999) / 1_000_000,
	}
}
