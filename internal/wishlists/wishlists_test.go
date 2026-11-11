package wishlists

import (
	"testing"
	"time"

	"wishlist/internal/platform"
)

func TestDayStart(t *testing.T) {
	for _, tc := range []struct {
		date     string
		zone     string
		startUTC string
		hours    int
	}{
		{"2026-11-10", "Europe/Moscow", "2026-11-09T21:00:00Z", 24},
		{"2027-03-14", "America/New_York", "2027-03-14T05:00:00Z", 23},
		{"2027-11-07", "America/New_York", "2027-11-07T04:00:00Z", 25},
		{"2018-11-04", "America/Sao_Paulo", "2018-11-04T03:00:00Z", 23},
		{"2011-12-30", "Pacific/Apia", "2011-12-30T10:00:00Z", 0},
		{"2040-12-31", "America/New_York", "2040-12-31T05:00:00Z", 24},
	} {
		t.Run(tc.zone+"/"+tc.date, func(t *testing.T) {
			date, err := time.Parse(time.DateOnly, tc.date)
			if err != nil {
				t.Fatal(err)
			}
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			start := platform.DayStart(date, loc)
			end := platform.DayStart(date.AddDate(0, 0, 1), loc)
			if start.Format(time.RFC3339) != tc.startUTC || end.Sub(start) != time.Duration(tc.hours)*time.Hour {
				t.Fatalf("got day [%s, %s), want %s for %d hours", start, end, tc.startUTC, tc.hours)
			}
		})
	}
}

func TestBookingClosesAtDeadline(t *testing.T) {
	closeAt := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)
	l := list{Summary: Summary{CloseAt: &closeAt}, ownerID: 1}
	if err := l.requireBooking(2, closeAt.Add(-time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if err := l.requireBooking(2, closeAt); err != errClosed {
		t.Fatalf("booking at close_at returned %v", err)
	}
}

func TestHolidayDateLimitUsesListTimeZone(t *testing.T) {
	// It is already March 1 in Moscow and still February 29 in UTC.
	now := time.Date(2028, time.February, 29, 22, 0, 0, 0, time.UTC)
	details := Details{Title: "Holiday", HolidayDate: "2029-02-28", TimeZone: "UTC"}
	if _, _, err := details.validate(now); err != nil {
		t.Fatalf("last allowed date: %v", err)
	}
	details.HolidayDate = "2029-03-01"
	if _, _, err := details.validate(now); err == nil {
		t.Fatal("accepted a date more than a calendar year away")
	}
	details.TimeZone = "Europe/Moscow"
	if _, _, err := details.validate(now); err != nil {
		t.Fatalf("same date should be allowed on March 1 in the list time zone: %v", err)
	}
}
