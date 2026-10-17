//go:build integration

package actions_test

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

func TestFailChatPGStore(t *testing.T) {
	failChatSuite(t, actions.NewPGStore(storetest.Pool(t), nil))
}
