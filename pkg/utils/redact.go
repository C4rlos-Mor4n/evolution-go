package utils

import (
	"strings"
	"unicode"
)

// MaskID masks phone numbers, JIDs and LIDs for logs, keeping only the last 4 digits of the
// user part so a line can still be correlated without exposing the identifier, e.g.
// "593900001234@s.whatsapp.net" -> "*******1234@s.whatsapp.net". Group/server parts are kept.
func MaskID(id string) string {
	if id == "" {
		return ""
	}
	user, rest := id, ""
	if i := strings.IndexAny(id, "@:."); i >= 0 {
		user, rest = id[:i], id[i:]
	}
	digits := 0
	for _, r := range user {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	if len(user) <= 4 || digits < len(user)/2 {
		// Not a phone/LID-like user part (or too short to mask meaningfully)
		if len(user) <= 4 {
			return strings.Repeat("*", len(user)) + rest
		}
		return user[:2] + strings.Repeat("*", len(user)-2) + rest
	}
	return strings.Repeat("*", len(user)-4) + user[len(user)-4:] + rest
}
