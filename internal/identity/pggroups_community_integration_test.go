//go:build integration

package identity

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

func TestPostgresGroupStoreCommunities(t *testing.T) {
	checkCommunityStore(t, NewPostgresGroupStore(storetest.Pool(t)))
}
