package whatsmeow_service

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestIsOwnAccountJID(t *testing.T) {
	ownPN := types.NewADJID("5511999999999", 0, 12)
	ownLID := types.NewADJID("123456789012345", 0, 12)
	ownLID.Server = types.HiddenUserServer

	cases := []struct {
		name string
		jid  types.JID
		want bool
	}{
		{"own PN without device", types.NewJID("5511999999999", types.DefaultUserServer), true},
		{"own PN other device", types.NewADJID("5511999999999", 0, 3), true},
		{"own LID", types.NewJID("123456789012345", types.HiddenUserServer), true},
		{"other contact", types.NewJID("5511888888888", types.DefaultUserServer), false},
		{"group", types.NewJID("120363000000000000", types.GroupServer), false},
		{"empty", types.EmptyJID, false},
	}
	for _, tc := range cases {
		if got := isOwnAccountJID(tc.jid, ownPN, ownLID); got != tc.want {
			t.Errorf("%s: isOwnAccountJID(%s) = %v, want %v", tc.name, tc.jid, got, tc.want)
		}
	}
	if isOwnAccountJID(types.NewJID("5511999999999", types.DefaultUserServer), types.EmptyJID, types.EmptyJID) {
		t.Error("unknown own identity must never match")
	}
}
