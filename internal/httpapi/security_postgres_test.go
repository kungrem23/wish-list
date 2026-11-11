package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wishlist/internal/history"
	"wishlist/internal/wishlists"
)

func TestPostgresOwnerWritesCheckOwnershipBeforeLock(t *testing.T) {
	f := setup(t)
	owner, outsider := f.register(t, "owner"), f.register(t, "outsider")
	list, gifts := f.newList(t, owner, `{"title":"A gift"}`)
	details := f.details(t, 5, "UTC")
	base, gift := ownerPath(list.ID), giftPath(list, gifts[0])

	tx, err := f.db.Begin(f.ctx)
	must(t, err)
	defer tx.Rollback(f.ctx)
	_, err = tx.Exec(f.ctx, "SELECT id FROM wishlists WHERE id = $1 FOR UPDATE", list.ID)
	must(t, err)

	quick, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	probe := f
	probe.ctx = quick
	missing := status(t, probe.call("PUT", ownerPath(list.ID+10000), outsider, details), 404).Body.String()
	for _, request := range []struct{ method, path, body string }{
		{"PUT", base, details},
		{"POST", base + "/copy", details},
		{"POST", base + "/gifts", `{"title":"New gift"}`},
		{"PUT", gift, `{"title":"New title"}`},
		{"DELETE", gift, ""},
		{"PUT", gift + "/outcome", `{"gifted":true}`},
	} {
		w := status(t, probe.call(request.method, request.path, outsider, request.body), 404)
		if w.Body.String() != missing {
			t.Fatalf("%s %s exposed the list's existence", request.method, request.path)
		}
	}
	status(t, f.call("PUT", base, owner, details), 503)
	must(t, tx.Commit(f.ctx))
	status(t, f.call("PUT", base, owner, details), 204)
}

func TestPostgresConcurrentQuotas(t *testing.T) {
	f := setup(t)
	owner := f.register(t, "owner")
	archived, _ := f.newList(t, owner)
	list, _ := f.newList(t, owner)
	details := f.details(t, 5, "UTC")
	_, err := f.db.Exec(f.ctx, "UPDATE wishlists SET archived_at = clock_timestamp() WHERE id = $1", archived.ID)
	must(t, err)
	_, err = f.db.Exec(f.ctx, `
		INSERT INTO wishlists (owner_id, title, holiday_date, time_zone, share_token, close_at)
		SELECT w.owner_id, 'Quota list', w.holiday_date, w.time_zone, 'quota-' || n, w.close_at
		FROM wishlists w CROSS JOIN generate_series(1, 47) n WHERE w.id = $1
	`, list.ID)
	must(t, err)
	checkConcurrent := func(first, second func() *httptest.ResponseRecorder, success int) {
		t.Helper()
		responses := make(chan *httptest.ResponseRecorder, 2)
		go func() { responses <- first() }()
		go func() { responses <- second() }()
		a, b := <-responses, <-responses
		if a.Code == 409 {
			a, b = b, a
		}
		status(t, a, success)
		status(t, b, 409)
	}
	checkConcurrent(
		func() *httptest.ResponseRecorder { return f.call("POST", "/wishlists", owner, details) },
		func() *httptest.ResponseRecorder {
			return f.call("POST", ownerPath(archived.ID)+"/copy", owner, details)
		},
		201,
	)
	_, err = f.db.Exec(f.ctx, `
		INSERT INTO gifts (wishlist_id, title, active)
		SELECT $1, 'Quota gift', n = 1 FROM generate_series(1, 99) n
	`, list.ID)
	must(t, err)
	addGift := func() *httptest.ResponseRecorder {
		return f.call("POST", ownerPath(list.ID)+"/gifts", owner, `{"title":"Last gift"}`)
	}
	checkConcurrent(addGift, addGift, 204)
	view := decode[wishlists.View](t, status(t, f.call("GET", ownerPath(list.ID), owner, ""), 200))
	status(t, f.call("PUT", giftPath(list, view.Gifts[0]), owner, `{"title":"Edited gift"}`), 204)
	status(t, f.call("DELETE", giftPath(list, view.Gifts[0]), owner, ""), 204)
	status(t, addGift(), 409)

	_, err = f.db.Exec(f.ctx, `
		INSERT INTO gifts (wishlist_id, title, visible_in_archive, gifted)
		SELECT $1, 'Legacy gift', true, false FROM generate_series(1, 101)
	`, archived.ID)
	must(t, err)
	response := status(t, f.call("POST", ownerPath(archived.ID)+"/copy", owner, details), 409)
	if !strings.Contains(response.Body.String(), "100 карточек") {
		t.Fatal(response.Body)
	}
}

func TestPostgresPaginationKeepsLegacyDataReachable(t *testing.T) {
	f := setup(t)
	owner, friend := f.register(t, "owner"), f.register(t, "friend")
	list, _ := f.newList(t, owner)
	_, err := f.db.Exec(f.ctx, `
		INSERT INTO wishlists (owner_id, title, holiday_date, time_zone, share_token, close_at, archived_at)
		SELECT w.owner_id, 'Legacy list', w.holiday_date, w.time_zone, 'legacy-' || n, w.close_at,
		       CASE WHEN n > 52 THEN clock_timestamp() END
		FROM wishlists w CROSS JOIN generate_series(1, 54) n WHERE w.id = $1
	`, list.ID)
	must(t, err)
	_, err = f.db.Exec(f.ctx, `
		WITH cards AS (
			INSERT INTO gifts (wishlist_id, title) SELECT $1, 'Legacy gift' FROM generate_series(1, 55)
			RETURNING id
		)
		INSERT INTO reservations (gift_id, user_id, reserved_at)
		SELECT id, (SELECT id FROM users WHERE email = 'friend@example.org'), statement_timestamp() FROM cards
	`, list.ID)
	must(t, err)

	firstLists, secondLists := testPages[[]wishlists.Summary](t, f, owner, "/wishlists")
	if len(firstLists) != 50 || len(secondLists) != 5 || firstLists[49].ID >= secondLists[0].ID ||
		secondLists[4].ArchivedAt == nil {
		t.Fatal("wishlists did not paginate in their display order")
	}
	firstProfile, secondProfile := testPages[wishlists.FriendProfile](t, f, friend, shared(list)+"/profile")
	if len(firstProfile.Lists) != 50 || len(secondProfile.Lists) != 3 ||
		firstProfile.Lists[49].ID >= secondProfile.Lists[0].ID || firstProfile.Lists[0].ID != list.ID || firstProfile.Lists[0].GiftCount != 55 {
		t.Fatal("shared profile lists did not paginate in their display order")
	}
	firstView, secondView := testPages[wishlists.View](t, f, friend, shared(list))
	if len(firstView.Gifts) != 50 || len(secondView.Gifts) != 5 || firstView.Gifts[49].ID >= secondView.Gifts[0].ID ||
		secondView.Gifts[0].Reservation != "mine" {
		t.Fatal("shared gifts did not paginate with reservation visibility")
	}
	firstOwner, secondOwner := testPages[wishlists.View](t, f, owner, ownerPath(list.ID))
	if len(firstOwner.Gifts) != 50 || len(secondOwner.Gifts) != 5 || secondOwner.Gifts[0].Reservation != "" {
		t.Fatal("owner gifts did not paginate with owner visibility")
	}
	firstBookings, secondBookings := testPages[[]wishlists.Booking](t, f, friend, "/reservations")
	if len(firstBookings) != 50 || len(secondBookings) != 5 || firstBookings[49].GiftID <= secondBookings[0].GiftID {
		t.Fatal("reservations with matching timestamps did not paginate")
	}

	f.setClose(t, list.ID, "-1 second")
	count, err := (wishlists.Store{DB: f.db}).ArchiveDue(f.ctx)
	must(t, err)
	if count != 1 {
		t.Fatalf("expected to archive one list, got %d", count)
	}
	firstHistory, secondHistory := testPages[[]history.Entry](t, f, friend, "/history")
	if len(firstHistory) != 50 || len(secondHistory) != 5 || firstHistory[49].GiftID <= secondHistory[0].GiftID ||
		!secondHistory[0].Gifted {
		t.Fatal("history with matching timestamps did not paginate")
	}
	status(t, f.call("GET", "/history?after=invalid!", friend, ""), 400)
}

func TestPostgresArchiveContinuesAcrossBatches(t *testing.T) {
	f := setup(t)
	owner := f.register(t, "owner")
	list, _ := f.newList(t, owner)
	f.setClose(t, list.ID, "-1 second")
	_, err := f.db.Exec(f.ctx, `
		INSERT INTO wishlists (owner_id, title, holiday_date, time_zone, share_token, close_at)
		SELECT w.owner_id, 'Due list', w.holiday_date, w.time_zone, 'due-' || n, w.close_at
		FROM wishlists w CROSS JOIN generate_series(1, 100) n WHERE w.id = $1
	`, list.ID)
	must(t, err)
	count, err := (wishlists.Store{DB: f.db}).ArchiveDue(f.ctx)
	must(t, err)
	if count != 101 {
		t.Fatalf("expected to archive both batches, got %d lists", count)
	}
}

func testPages[T any](t *testing.T, f fixture, session *http.Cookie, path string) (T, T) {
	t.Helper()
	first := status(t, f.call("GET", path, session, ""), 200)
	cursor := first.Header().Get("X-Next-Cursor")
	if cursor == "" {
		t.Fatalf("%s omitted the cursor for the next page", path)
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	second := status(t, f.call("GET", path+separator+"after="+cursor, session, ""), 200)
	if second.Header().Get("X-Next-Cursor") != "" {
		t.Fatalf("%s returned a cursor after the last page", path)
	}
	return decode[T](t, first), decode[T](t, second)
}
