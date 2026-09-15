package backend

import (
	"sync"
	"time"
)

// Process-wide "configured display timezone" for the calendar.
//
// All-day (VALUE=DATE) and floating (no-TZID) event times have no timezone of
// their own, so they must be anchored to SOME zone. The frontend buckets days
// by the user's CONFIGURED display timezone (calendarSettings.effectiveTimezone),
// so the backend must interpret these tz-less values in that SAME zone — not the
// machine's system tz — or all-day events straddle two days when the two differ.
//
// The create path already does this (it threads the tz via EventInput.TZName,
// commit cbea14f). The parse/sync path runs during background sync with no
// frontend call to carry the tz, so the configured zone is held here: seeded
// from the `meta` table at init and updated by Calendar_SetDisplayTimezone. The
// leaf functions that interpret tz-less times (buildEvent, buildOverride,
// resolveLocation, setDateValue) read it via configuredTZ(). When unset it
// returns time.Local — identical to the prior behavior for users whose display
// tz matches their system tz (the common case).
var (
	cfgTZMu  sync.RWMutex
	cfgTZLoc *time.Location // nil → fall back to time.Local
)

// SetConfiguredTimezone sets the configured display timezone from an IANA name
// (e.g. "America/Los_Angeles"). An empty or invalid name clears the override,
// falling back to the system tz. Safe for concurrent use (sync goroutines read
// it while the Wails thread sets it).
func SetConfiguredTimezone(tzName string) {
	var loc *time.Location
	if tzName != "" {
		if l, err := time.LoadLocation(tzName); err == nil {
			loc = l
		}
	}
	cfgTZMu.Lock()
	cfgTZLoc = loc
	cfgTZMu.Unlock()
}

// configuredTZ returns the configured display timezone, or time.Local when none
// is set. Never returns nil.
func configuredTZ() *time.Location {
	cfgTZMu.RLock()
	loc := cfgTZLoc
	cfgTZMu.RUnlock()
	if loc == nil {
		return time.Local
	}
	return loc
}
