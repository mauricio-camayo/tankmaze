package db

import "strings"

// RealTankID strips the "#N" suffix that Game Day autofill (item 248) appends
// to a MatchTank.TankID when the same built-in AI is registered more than
// once in one event, e.g. "builtin-scout#2". The suffix exists purely to give
// each duplicated registration its own key for that event's internal
// standings/seeding/bracket-tier bookkeeping (see tournament-scheduler's
// handleRegistrationClose) — it is never a real Tank or TankVersion record.
// Any caller that fetches a Tank/TankVersion or writes a permanent Ranking
// must strip it back to the real ID first, or the lookup/write will target a
// row that doesn't exist.
func RealTankID(id string) string {
	if i := strings.IndexByte(id, '#'); i >= 0 {
		return id[:i]
	}
	return id
}
