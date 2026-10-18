package main

import (
	"net/url"
	"strconv"
	"strings"
)

// minPoolConns is the pool size the process needs: the inbox workers, the executors, the LISTEN
// connections (settings, rules) and, inside a message's transaction, the review service's group
// lookups each hold a connection at once. pgxpool's default (max(4, CPUs)) can deadlock that.
const minPoolConns = 20

// withPoolFloor sets pool_max_conns on a Postgres connection string unless the operator already
// chose one. It accepts both URL (postgres://…) and keyword (host=… dbname=…) forms.
func withPoolFloor(dsn string, n int) string {
	if strings.Contains(dsn, "pool_max_conns") || strings.TrimSpace(dsn) == "" {
		return dsn
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn // let the driver report the bad URL
		}
		q := u.Query()
		q.Set("pool_max_conns", strconv.Itoa(n))
		u.RawQuery = q.Encode()
		return u.String()
	}
	return strings.TrimSpace(dsn) + " pool_max_conns=" + strconv.Itoa(n)
}
