package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"wishlist/internal/wishlists"

	"wishlist/internal/problem"
)

const pageSize = 50

var errCursor = problem.New(http.StatusBadRequest, "Некорректный указатель страницы")

type listCursor struct {
	Archived bool
	Date     string
	ID       int64
}

func readListCursor(r *http.Request) (listCursor, error) {
	cursor, err := readCursor[listCursor](r)
	if err != nil {
		return cursor, err
	}
	if cursor.ID < 0 || (cursor.ID == 0 && (cursor.Archived || cursor.Date != "")) {
		return cursor, errCursor
	}
	if cursor.Date != "" {
		if date, err := time.Parse(time.DateOnly, cursor.Date); err != nil || date.Year() < 1 {
			return cursor, errCursor
		}
	}
	return cursor, nil
}

func readGiftCursor(r *http.Request) (wishlists.GiftCursor, error) {
	sort := r.URL.Query().Get("sort")
	if sort == "" {
		sort = wishlists.SortPriority
	}
	switch sort {
	case wishlists.SortPriority, wishlists.SortName, wishlists.SortAdded:
	default:
		return wishlists.GiftCursor{}, problem.New(http.StatusBadRequest, "Некорректная сортировка подарков")
	}
	if r.URL.Query().Get("after") == "" {
		return wishlists.GiftCursor{Sort: sort}, nil
	}
	cursor, err := readCursor[wishlists.GiftCursor](r)
	if err != nil || cursor.Sort != sort || cursor.ID <= 0 {
		return cursor, errCursor
	}
	switch sort {
	case wishlists.SortPriority:
		if cursor.Priority != "high" && cursor.Priority != "normal" && cursor.Priority != "low" {
			return cursor, errCursor
		}
	case wishlists.SortName:
		if cursor.Title == "" || utf8.RuneCountInString(cursor.Title) > 150 || strings.ContainsRune(cursor.Title, 0) {
			return cursor, errCursor
		}
	case wishlists.SortAdded:
		if cursor.CreatedAt == nil {
			return cursor, errCursor
		}
	}
	return cursor, nil
}

type activityCursor struct {
	At *time.Time
	ID int64
}

func readCursor[T any](r *http.Request) (T, error) {
	var zero T
	raw := r.URL.Query().Get("after")
	if raw == "" {
		return zero, nil
	}
	if len(raw) > 2048 {
		return zero, errCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	var cursor *T
	if err != nil || json.Unmarshal(data, &cursor) != nil || cursor == nil {
		return zero, errCursor
	}
	return *cursor, nil
}

func page[T any, C any](w http.ResponseWriter, items []T, cursor func(T) C) []T {
	if len(items) > pageSize {
		items = items[:pageSize]
		data, err := json.Marshal(cursor(items[pageSize-1]))
		if err == nil {
			w.Header().Set("X-Next-Cursor", base64.RawURLEncoding.EncodeToString(data))
		}
	}
	return items
}
