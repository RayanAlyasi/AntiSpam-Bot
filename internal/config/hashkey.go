package config

import (
	"errors"
	"fmt"
)

// MinHashKeyBytes is the shortest HASH_KEY accepted.
const MinHashKeyBytes = 16

// ErrHashKey means HASH_KEY is missing or too short to run the bot.
var ErrHashKey = errors.New("HASH_KEY")

// NumberHashKey returns the deployment's key for the keyed phone-number hashes (DESIGN.md §7.5,
// §9.8, §14 number_blacklist.pn_hash): blacklisted and advertised numbers are matched by these
// hashes, so the tables never need the number in the clear. It is required to run the bot, and
// it must never change once numbers are stored, or stored entries stop matching. There is no
// default and none is generated: a key made up at start would change on every restart.
// Errors never contain the key.
func (c Config) NumberHashKey() ([]byte, error) {
	switch {
	case c.HashKey == "":
		return nil, fmt.Errorf("%w is required: set it to a random secret of at least %d bytes, e.g. `openssl rand -hex 32`, and keep it: changing it later makes stored blacklist and corpus numbers stop matching", ErrHashKey, MinHashKeyBytes)
	case len(c.HashKey) < MinHashKeyBytes:
		return nil, fmt.Errorf("%w is too short (%d bytes): use a random secret of at least %d bytes, e.g. `openssl rand -hex 32`", ErrHashKey, len(c.HashKey), MinHashKeyBytes)
	}
	return []byte(c.HashKey), nil
}
