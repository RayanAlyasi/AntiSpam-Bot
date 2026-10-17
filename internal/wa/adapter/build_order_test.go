package adapter

import (
	"slices"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// Texts read by reflection come out in proto declaration order, whatever order the fields
// were set in. protobuf-go randomizes Message.Range per binary, and the text order feeds
// normalization, fingerprints and hashes, so it must not change between builds or restarts.
func TestReflectedTextsFollowDeclarationOrder(t *testing.T) {
	tests := []struct {
		name string
		msg  *waE2E.Message
		want []string
	}{
		{"list message", &waE2E.Message{ListMessage: &waE2E.ListMessage{
			FooterText: s("footer"),
			Sections: []*waE2E.ListMessage_Section{{
				Rows:  []*waE2E.ListMessage_Row{{RowID: s("r1"), Description: s("row desc"), Title: s("row title")}},
				Title: s("section"),
			}},
			ButtonText:  s("Open"),
			Description: s("list desc"),
			Title:       s("List title"),
		}}, []string{"List title", "list desc", "Open", "section", "row title", "row desc", "footer"}},
		{"buttons message", &waE2E.Message{ButtonsMessage: &waE2E.ButtonsMessage{
			Buttons:     []*waE2E.ButtonsMessage_Button{{ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{DisplayText: s("Claim")}, ButtonID: s("b1")}},
			FooterText:  s("footer"),
			ContentText: s("Pick one"),
		}}, []string{"Pick one", "footer", "Claim"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var first []string
			for i := range 20 {
				got := texts(build(t, tt.msg))["interactive"]
				if i == 0 {
					first = got
				} else if !slices.Equal(got, first) {
					t.Fatalf("run %d gave %q, run 0 gave %q: extraction order must be stable", i, got, first)
				}
			}
			if !slices.Equal(first, tt.want) {
				t.Errorf("texts = %q, want %q (proto declaration order)", first, tt.want)
			}
		})
	}
}
