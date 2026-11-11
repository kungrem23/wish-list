package platform

import (
	"net/http"
	"strings"
	"time"
	_ "time/tzdata"

	"wishlist/internal/problem"
)

// DayStart resolves the first instant of a local calendar day, including zones
// that repeat midnight or jump over it. The date is a calendar label in UTC.
func DayStart(date time.Time, loc *time.Location) time.Time {
	for instant := date.Add(-48 * time.Hour).In(loc); ; {
		_, offset := instant.Zone()
		_, end := instant.ZoneBounds()
		midnight := date.Add(-time.Duration(offset) * time.Second)
		if !midnight.After(instant) {
			return instant.UTC()
		}
		// Extrapolated POSIX zone rules can return a year-end bound that
		// omits the leap day. Advance to the next year if that bound passed.
		if !end.IsZero() && !end.After(instant) {
			end = time.Date(instant.UTC().Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)
		}
		if end.IsZero() || midnight.Before(end) {
			return midnight
		}
		instant = end.In(loc)
	}
}

// CalendarDay validates a date and returns its label, zone and UTC boundaries.
func CalendarDay(dateLabel, zone string) (time.Time, *time.Location, time.Time, time.Time, error) {
	date, err := time.Parse(time.DateOnly, dateLabel)
	if err != nil || date.Year() < 1 {
		return time.Time{}, nil, time.Time{}, time.Time{}, problem.New(http.StatusBadRequest, "Укажите корректную дату праздника")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" || zone == "localtime" ||
		zone == "posixrules" || strings.HasPrefix(zone, "posix/") || strings.HasPrefix(zone, "right/") {
		return time.Time{}, nil, time.Time{}, time.Time{}, problem.New(http.StatusBadRequest, "Укажите часовой пояс IANA, например Europe/Moscow")
	}
	start := DayStart(date, loc)
	closeAt := DayStart(date.AddDate(0, 0, 1), loc)
	if closeAt.Year() > 9999 {
		return time.Time{}, nil, time.Time{}, time.Time{}, problem.New(http.StatusBadRequest, "Дата закрытия выходит за пределы поддерживаемого диапазона")
	}
	if start.Equal(closeAt) {
		return time.Time{}, nil, time.Time{}, time.Time{}, problem.New(http.StatusBadRequest, "Эта календарная дата пропущена в выбранном часовом поясе")
	}
	return date, loc, start, closeAt, nil
}
