package backend

import (
	"testing"
	"time"
)

const allDayICS = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:test-allday\r\n" +
	"DTSTART;VALUE=DATE:20260118\r\n" +
	"DTEND;VALUE=DATE:20260119\r\n" +
	"SUMMARY:Test All Day\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

// A VALUE=DATE all-day event must be anchored to the CONFIGURED display tz, not
// the system tz — so it buckets on the right day in the UI. This also verifies
// go-ical's DateTimeStart honors the supplied *time.Location for DATE values.
func TestParseAllDayUsesConfiguredTimezone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("tz data unavailable: %v", err)
	}

	SetConfiguredTimezone("America/Los_Angeles")
	t.Cleanup(func() { SetConfiguredTimezone("") })

	parsed, err := ParseCalendarObject(allDayICS)
	if err != nil {
		t.Fatalf("ParseCalendarObject: %v", err)
	}
	if !parsed.Master.IsAllDay {
		t.Fatal("expected IsAllDay=true")
	}
	want := time.Date(2026, 1, 18, 0, 0, 0, 0, la).Unix()
	if parsed.Master.DTStartUnix != want {
		t.Errorf("DTStartUnix = %d (%s), want %d (LA midnight 2026-01-18)",
			parsed.Master.DTStartUnix,
			time.Unix(parsed.Master.DTStartUnix, 0).In(la), want)
	}
}

func TestConfiguredTZFallsBackToLocalWhenUnset(t *testing.T) {
	SetConfiguredTimezone("")
	if got := configuredTZ(); got != time.Local {
		t.Errorf("configuredTZ() = %v, want time.Local when unset", got)
	}

	SetConfiguredTimezone("America/Los_Angeles")
	t.Cleanup(func() { SetConfiguredTimezone("") })
	if got := configuredTZ(); got.String() != "America/Los_Angeles" {
		t.Errorf("configuredTZ() = %v, want America/Los_Angeles", got)
	}
}
