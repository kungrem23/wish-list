package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wishlist/internal/history"
	"wishlist/internal/httpapi"
	"wishlist/internal/platform"
	"wishlist/internal/wishlists"
)

type fixture struct {
	ctx     context.Context
	db      *pgxpool.Pool
	handler http.Handler
	schema  string
}

func setup(t *testing.T) fixture {
	t.Helper()
	return setupThrough(t, "")
}

func setupThrough(t *testing.T, last string) fixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, url)
	must(t, err)
	t.Cleanup(admin.Close)
	schema := "test_wishlist_" + strings.ToLower(platform.Token()[:12])
	schema = strings.ReplaceAll(schema, "-", "_")
	name := pgx.Identifier{schema}.Sanitize()
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+name)
	must(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Error(err)
		}
	})

	config, err := pgxpool.ParseConfig(url)
	must(t, err)
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.RuntimeParams["application_name"] = schema
	config.MaxConns = 12
	db, err := pgxpool.NewWithConfig(ctx, config)
	must(t, err)
	t.Cleanup(db.Close)
	migrations, err := filepath.Glob("../../migrations/*.sql")
	must(t, err)
	if len(migrations) == 0 {
		t.Fatal("no migrations found")
	}
	for _, path := range migrations {
		applyMigration(t, ctx, db, path)
		if filepath.Base(path) == last {
			break
		}
	}
	return fixture{ctx: ctx, db: db, handler: httpapi.Handler(db, false), schema: schema}
}

func applyMigration(t *testing.T, ctx context.Context, db *pgxpool.Pool, path string) {
	t.Helper()
	migration, err := os.ReadFile(path)
	must(t, err)
	_, err = db.Exec(ctx, string(migration))
	must(t, err)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f fixture) call(method, path string, session *http.Cookie, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body)).WithContext(f.ctx)
	if session != nil {
		r.AddCookie(session)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func status(t *testing.T, w *httptest.ResponseRecorder, expected int) *httptest.ResponseRecorder {
	t.Helper()
	if w.Code != expected {
		t.Fatalf("want status %d, got %d: %s", expected, w.Code, w.Body)
	}
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	must(t, json.Unmarshal(w.Body.Bytes(), &value))
	return value
}

func (f fixture) register(t *testing.T, name string) *http.Cookie {
	t.Helper()
	w := status(t, f.call("POST", "/register", nil,
		fmt.Sprintf(`{"email":%q,"display_name":%q,"password":"mypassword"}`, name+"@example.org", name)), 204)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected session cookie: %v", cookies)
	}
	return cookies[0]
}

func (f fixture) details(t *testing.T, days int, zone string) string {
	t.Helper()
	var now time.Time
	must(t, f.db.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now))
	loc, err := time.LoadLocation(zone)
	must(t, err)
	date := now.In(loc).AddDate(0, 0, days).Format(time.DateOnly)
	return fmt.Sprintf(`{"title":"Holiday","holiday_date":%q,"time_zone":%q}`, date, zone)
}

func (f fixture) newList(t *testing.T, owner *http.Cookie, cards ...string) (wishlists.Summary, []wishlists.Gift) {
	t.Helper()
	list := decode[wishlists.Summary](t, status(t, f.call("POST", "/wishlists", owner, f.details(t, 4, "UTC")), 201))
	for _, card := range cards {
		status(t, f.call("POST", ownerPath(list.ID)+"/gifts", owner, card), 204)
	}
	view := decode[wishlists.View](t, status(t, f.call("GET", ownerPath(list.ID), owner, ""), 200))
	return list, view.Gifts
}

func ownerPath(id int64) string         { return fmt.Sprintf("/wishlists/%d", id) }
func shared(l wishlists.Summary) string { return "/shared/" + l.ShareToken }
func giftPath(l wishlists.Summary, g wishlists.Gift) string {
	return fmt.Sprintf("%s/gifts/%d", ownerPath(l.ID), g.ID)
}
func booking(l wishlists.Summary, g wishlists.Gift) string {
	return fmt.Sprintf("%s/gifts/%d/reservation", shared(l), g.ID)
}

func (f fixture) setClose(t *testing.T, id int64, offset string) time.Time {
	t.Helper()
	var closeAt time.Time
	err := f.db.QueryRow(f.ctx, `
		UPDATE wishlists SET close_at = clock_timestamp() + $2::text::interval WHERE id = $1 RETURNING close_at
	`, id, offset).Scan(&closeAt)
	must(t, err)
	return closeAt
}

func TestPostgresWishlistLifecycle(t *testing.T) {
	f := setup(t)
	owner := f.register(t, "Boris")
	anna := f.register(t, "Anna")
	friend := f.register(t, "Daria")
	status(t, f.call("POST", "/login", nil, `{"email":"BORIS@example.org","password":"wrong"}`), 401)
	w := status(t, f.call("POST", "/login", nil, `{"email":"BORIS@example.org","password":"mypassword"}`), 204)
	login := w.Result().Cookies()[0]
	status(t, f.call("GET", "/me", login, ""), 200)
	status(t, f.call("POST", "/logout", login, ""), 204)
	status(t, f.call("GET", "/me", login, ""), 401)
	profile := status(t, f.call("PUT", "/me", owner,
		`{"clothing_size":"XL","shoe_size_eu":"42.5","birth_date":"1990-11-10"}`), 200)
	if !strings.Contains(profile.Body.String(), `"shoe_size_eu":"42.5"`) {
		t.Fatal(profile.Body)
	}
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format(time.DateOnly)
	status(t, f.call("PUT", "/me", owner, fmt.Sprintf(`{"birth_date":%q}`, tomorrow)), 400)

	list, gifts := f.newList(t, owner,
		`{"title":"Book","description":"Original edition","product_url":"https://marketplace.invalid/item?variant=1"}`,
		`{"title":"Hidden and reserved"}`, `{"title":"Hidden without booking"}`, `{"title":"Hidden then cancelled"}`,
		`{"title":"Ungifted"}`)
	status(t, f.call("GET", shared(list), nil, ""), 401)
	status(t, f.call("GET", ownerPath(list.ID), anna, ""), 404)
	before := status(t, f.call("GET", shared(list), owner, ""), 200).Body.String()
	guest := status(t, f.call("GET", shared(list), anna, ""), 200).Body.String()
	if strings.Contains(guest, "42.5") || strings.Contains(guest, "1990-11-10") || strings.Contains(guest, "clothing_size") {
		t.Fatalf("profile leaked: %s", guest)
	}

	type attempt struct {
		cookie *http.Cookie
		result *httptest.ResponseRecorder
	}
	start := make(chan struct{})
	results := make(chan attempt, 2)
	for _, cookie := range []*http.Cookie{anna, friend} {
		go func() {
			<-start
			results <- attempt{cookie, f.call("POST", booking(list, gifts[0]), cookie, "")}
		}()
	}
	close(start)
	winner, loser := <-results, <-results
	if winner.result.Code != 204 {
		winner, loser = loser, winner
	}
	status(t, winner.result, 204)
	status(t, loser.result, 409)
	if !strings.Contains(loser.result.Body.String(), "Подарок уже забронирован") {
		t.Fatal(loser.result.Body)
	}
	after := status(t, f.call("GET", shared(list), owner, ""), 200).Body.String()
	own := status(t, f.call("GET", ownerPath(list.ID), owner, ""), 200).Body.String()
	if before != after || before != own || strings.Contains(own, `"reservation":`) || strings.Contains(own, `"gifted":`) {
		t.Fatalf("owner received different or sensitive data: %s -> %s", before, after)
	}
	status(t, f.call("DELETE", booking(list, gifts[0]), owner, ""), 403)
	status(t, f.call("DELETE", booking(list, gifts[0]), loser.cookie, ""), 204)
	status(t, f.call("DELETE", booking(list, gifts[4]), loser.cookie, ""), 204)
	var count int
	must(t, f.db.QueryRow(f.ctx, "SELECT count(*) FROM reservations WHERE gift_id = $1", gifts[0].ID).Scan(&count))
	if count != 1 {
		t.Fatalf("want one surviving reservation, got %d", count)
	}
	taken := status(t, f.call("GET", shared(list), loser.cookie, ""), 200)
	if !strings.Contains(taken.Body.String(), `"reservation":"taken"`) ||
		strings.Contains(taken.Body.String(), `"Anna"`) || strings.Contains(taken.Body.String(), `"Daria"`) {
		t.Fatalf("invalid guest view: %s", taken.Body)
	}
	status(t, f.call("DELETE", booking(list, gifts[0]), winner.cookie, ""), 204)
	status(t, f.call("POST", booking(list, gifts[0]), anna, ""), 204)

	for _, i := range []int{1, 3} {
		status(t, f.call("POST", booking(list, gifts[i]), anna, ""), 204)
	}
	for _, i := range []int{1, 2, 3} {
		status(t, f.call("DELETE", giftPath(list, gifts[i]), owner, ""), 204)
		status(t, f.call("POST", booking(list, gifts[i]), friend, ""), 409)
	}
	annaView := decode[wishlists.View](t, status(t, f.call("GET", shared(list), anna, ""), 200))
	if len(annaView.Gifts) != 4 || annaView.Gifts[1].Active || annaView.Gifts[1].Reservation != "mine" {
		t.Fatalf("hidden gift missing from reserver's view: %+v", annaView.Gifts)
	}
	bookings := decode[[]wishlists.Booking](t, status(t, f.call("GET", "/reservations", anna, ""), 200))
	if len(bookings) != 3 {
		t.Fatalf("unexpected bookings: %+v", bookings)
	}
	// Cancel using the list token and gift ID from the dashboard.
	dashboard := bookings[0]
	if dashboard.GiftID != gifts[3].ID || dashboard.Active || !dashboard.CanCancel {
		t.Fatalf("expected a cancellable hidden gift: %+v", dashboard)
	}
	status(t, f.call("DELETE", fmt.Sprintf("/shared/%s/gifts/%d/reservation", dashboard.ShareToken, dashboard.GiftID), anna, ""), 204)

	status(t, f.call("PUT", ownerPath(list.ID), owner, f.details(t, 2, "Europe/Moscow")), 204)
	view := decode[wishlists.View](t, status(t, f.call("GET", ownerPath(list.ID), owner, ""), 200))
	if view.HolidayDate >= list.HolidayDate || view.TimeZone != "Europe/Moscow" || view.CloseAt.In(time.FixedZone("MSK", 3*3600)).Hour() != 0 {
		t.Fatalf("unexpected reschedule: %+v", view.Summary)
	}

	_, err := f.db.Exec(f.ctx, `UPDATE wishlists SET holiday_date = (clock_timestamp() AT TIME ZONE 'Europe/Moscow')::date,
		close_at = ((clock_timestamp() AT TIME ZONE 'Europe/Moscow')::date + 1)::timestamp AT TIME ZONE 'Europe/Moscow'
		WHERE id = $1`, list.ID)
	must(t, err)
	day := decode[wishlists.View](t, status(t, f.call("GET", ownerPath(list.ID), owner, ""), 200))
	if day.Phase != "holiday" {
		t.Fatalf("unexpected phase: %s", day.Phase)
	}
	status(t, f.call("PUT", ownerPath(list.ID), owner, f.details(t, 5, "UTC")), 409)
	status(t, f.call("PUT", giftPath(list, gifts[0]), owner, `{"title":"New title"}`), 409)
	status(t, f.call("DELETE", giftPath(list, gifts[0]), owner, ""), 409)
	status(t, f.call("POST", ownerPath(list.ID)+"/gifts", owner, `{"title":"Late"}`), 409)
	status(t, f.call("POST", booking(list, gifts[4]), friend, ""), 204)
	status(t, f.call("DELETE", booking(list, gifts[4]), friend, ""), 204)

	missed, _ := f.newList(t, owner)
	f.setClose(t, missed.ID, "-3 days")
	f.setClose(t, list.ID, "-1 second")
	status(t, f.call("POST", booking(list, gifts[4]), friend, ""), 409)
	status(t, f.call("DELETE", booking(list, gifts[1]), anna, ""), 409)
	closed := status(t, f.call("GET", ownerPath(list.ID), owner, ""), 200).Body.String()
	if !strings.Contains(closed, "awaiting_archive") || strings.Contains(closed, `"gifted":`) || strings.Contains(closed, `"reservation":`) {
		t.Fatalf("invalid pending archive response: %s", closed)
	}

	store := wishlists.Store{DB: f.db}
	total := 0
	archives := make(chan archiveResult, 2)
	go f.archive(archives)
	go f.archive(archives)
	for range 2 {
		result := <-archives
		must(t, result.err)
		total += result.count
	}
	if total != 2 {
		t.Fatalf("two processes should archive both lists exactly once, got %d", total)
	}
	archive := decode[wishlists.View](t, status(t, f.call("GET", ownerPath(list.ID), owner, ""), 200))
	if archive.Phase != "archived" || len(archive.Gifts) != 3 ||
		archive.Gifts[1].ID != gifts[1].ID || archive.Gifts[1].Active ||
		!*archive.Gifts[0].Gifted || !*archive.Gifts[1].Gifted || *archive.Gifts[2].Gifted {
		t.Fatalf("invalid archive: %+v", archive)
	}
	status(t, f.call("PUT", giftPath(list, gifts[2])+"/outcome", owner, `{"gifted":true}`), 404)
	status(t, f.call("PUT", giftPath(list, gifts[0])+"/outcome", owner, `{"gifted":false}`), 204)
	status(t, f.call("PUT", giftPath(list, gifts[1])+"/outcome", owner, `{"gifted":false}`), 204)
	count, err = store.ArchiveDue(f.ctx)
	must(t, err)
	if count != 0 {
		t.Fatalf("repeated archive processed %d lists", count)
	}
	entries := decode[[]history.Entry](t, status(t, f.call("GET", "/history", anna, ""), 200))
	if len(entries) != 2 || entries[0].Gifted || entries[1].Gifted || entries[0].Recipient != "Boris" {
		t.Fatalf("history should reflect the corrected outcomes: %+v", entries)
	}
	for _, entry := range entries {
		for _, original := range bookings {
			if entry.GiftID == original.GiftID && !entry.ReservedAt.Equal(original.ReservedAt) {
				t.Fatalf("reservation time changed: %v != %v", entry.ReservedAt, original.ReservedAt)
			}
		}
	}
	historyPath := fmt.Sprintf("/history/%d", gifts[0].ID)
	status(t, f.call("DELETE", historyPath, friend, ""), 204)
	status(t, f.call("DELETE", historyPath, anna, ""), 204)
	entries = decode[[]history.Entry](t, status(t, f.call("GET", "/history", anna, ""), 200))
	if len(entries) != 1 || entries[0].GiftID != gifts[1].ID {
		t.Fatalf("unexpected history after personal deletion: %+v", entries)
	}

	// Keep one visible gifted item so copying exercises both filters.
	status(t, f.call("PUT", giftPath(list, gifts[4])+"/outcome", owner, `{"gifted":true}`), 204)
	copy := decode[wishlists.Summary](t, status(t,
		f.call("POST", ownerPath(list.ID)+"/copy", owner, f.details(t, 6, "UTC")), 201))
	copied := decode[wishlists.View](t, status(t, f.call("GET", ownerPath(copy.ID), owner, ""), 200))
	if copied.ShareToken == list.ShareToken || copied.Phase != "editing" || len(copied.Gifts) != 2 {
		t.Fatalf("unexpected copied list: %+v", copied)
	}
	for i, g := range copied.Gifts {
		if g.ID == gifts[i].ID || g.Title != gifts[i].Title || !g.Active || g.Gifted != nil {
			t.Fatalf("invalid copy: %+v", g)
		}
	}
	if copied.Gifts[0].ProductURL == nil || *copied.Gifts[0].ProductURL != *gifts[0].ProductURL {
		t.Fatalf("product link changed: %+v", copied.Gifts[0])
	}
}

type archiveResult struct {
	count int
	err   error
}

func (f fixture) archive(out chan<- archiveResult) {
	count, err := (wishlists.Store{DB: f.db}).ArchiveDue(f.ctx)
	out <- archiveResult{count, err}
}

func (f fixture) waitForLocks(t *testing.T, waitEvent, queryPart string, want int) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		var count int
		err := f.db.QueryRow(f.ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE application_name = $1 AND wait_event_type = 'Lock' AND ($2 = '' OR wait_event = $2)
			  AND query LIKE '%' || $3 || '%'
		`, f.schema, waitEvent, queryPart).Scan(&count)
		must(t, err)
		if count >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not observe %d waiting queries for %q", want, queryPart)
}

func (f fixture) waitUntil(t *testing.T, at time.Time) {
	t.Helper()
	_, err := f.db.Exec(f.ctx, "SELECT pg_sleep(greatest(0, extract(epoch FROM $1::timestamptz - clock_timestamp())))", at)
	must(t, err)
}

func (f fixture) pauseWrites(t *testing.T, operation, table string) pgx.Tx {
	t.Helper()
	_, err := f.db.Exec(f.ctx, `
		CREATE FUNCTION pause_write() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(hashtext(current_schema()));
			IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
			RETURN NEW;
		END
		$$;
		CREATE TRIGGER pause_write BEFORE `+operation+` ON `+table+`
		FOR EACH ROW EXECUTE FUNCTION pause_write()
	`)
	must(t, err)
	tx, err := f.db.Begin(f.ctx)
	must(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	_, err = tx.Exec(f.ctx, "SELECT pg_advisory_xact_lock(hashtext(current_schema()))")
	must(t, err)
	return tx
}

func TestPostgresArchiveIncludesAdmittedWrites(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		method, operation := "POST", "INSERT"
		if cancel {
			method, operation = "DELETE", "DELETE"
		}
		t.Run(operation, func(t *testing.T) {
			f := setup(t)
			owner, friend := f.register(t, "owner"), f.register(t, "friend")
			list, gifts := f.newList(t, owner, `{"title":"A gift"}`)
			path := booking(list, gifts[0])
			if cancel {
				status(t, f.call("POST", path, friend, ""), 204)
			}
			blocker := f.pauseWrites(t, operation, "reservations")
			closeAt := f.setClose(t, list.ID, "1.5 seconds")
			response := make(chan *httptest.ResponseRecorder, 1)
			go func() { response <- f.call(method, path, friend, "") }()
			f.waitForLocks(t, "advisory", "", 1)
			f.waitUntil(t, closeAt)

			result := make(chan archiveResult, 2)
			go f.archive(result)
			go f.archive(result)
			f.waitForLocks(t, "", "WHERE id = $1 FOR UPDATE", 2)
			must(t, blocker.Commit(f.ctx))
			status(t, <-response, 204)
			first, second := <-result, <-result
			must(t, first.err)
			must(t, second.err)
			if first.count+second.count != 1 {
				t.Fatalf("expected exactly one archive commit, got %d and %d", first.count, second.count)
			}
			view := decode[wishlists.View](t, status(t, f.call("GET", ownerPath(list.ID), owner, ""), 200))
			entries := decode[[]history.Entry](t, status(t, f.call("GET", "/history", friend, ""), 200))
			wantHistory := 1
			if cancel {
				wantHistory = 0
			}
			if view.Gifts[0].Gifted == nil || *view.Gifts[0].Gifted != !cancel || len(entries) != wantHistory {
				t.Fatalf("archive missed the admitted %s: gifts=%+v history=%+v", method, view.Gifts, entries)
			}
		})
	}
}

func TestPostgresDeadlineIsCheckedAfterWaiting(t *testing.T) {
	f := setup(t)
	owner, friend := f.register(t, "owner"), f.register(t, "friend")
	list, gifts := f.newList(t, owner, `{"title":"Reserved"}`, `{"title":"Free"}`)
	status(t, f.call("POST", booking(list, gifts[0]), friend, ""), 204)
	closeAt := f.setClose(t, list.ID, "1.5 seconds")

	tx, err := f.db.Begin(f.ctx)
	must(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	_, err = tx.Exec(f.ctx, "SELECT id FROM wishlists WHERE id = $1 FOR UPDATE", list.ID)
	must(t, err)
	responses := make(chan *httptest.ResponseRecorder, 2)
	go func() { responses <- f.call("DELETE", booking(list, gifts[0]), friend, "") }()
	go func() { responses <- f.call("POST", booking(list, gifts[1]), friend, "") }()
	f.waitForLocks(t, "", "WHERE share_token = $1 FOR UPDATE", 2)
	f.waitUntil(t, closeAt)
	must(t, tx.Commit(f.ctx))
	for range 2 {
		w := status(t, <-responses, 409)
		if !strings.Contains(w.Body.String(), "Бронирование и отмена закрыты") {
			t.Fatal(w.Body)
		}
	}
	rows := decode[[]wishlists.Booking](t, status(t, f.call("GET", "/reservations", friend, ""), 200))
	if len(rows) != 1 || rows[0].GiftID != gifts[0].ID || rows[0].CanCancel {
		t.Fatalf("late request changed bookings: %+v", rows)
	}
}

func TestPostgresHideAndReserveSerialize(t *testing.T) {
	for _, hideFirst := range []bool{true, false} {
		operation, table := "INSERT", "reservations"
		if hideFirst {
			operation, table = "UPDATE", "gifts"
		}
		t.Run(operation, func(t *testing.T) {
			f := setup(t)
			owner, friend := f.register(t, "owner"), f.register(t, "friend")
			list, gifts := f.newList(t, owner, `{"title":"A gift"}`)
			blocker := f.pauseWrites(t, operation, table)
			hide := make(chan *httptest.ResponseRecorder, 1)
			reserve := make(chan *httptest.ResponseRecorder, 1)
			hideGift := func() { hide <- f.call("DELETE", giftPath(list, gifts[0]), owner, "") }
			reserveGift := func() { reserve <- f.call("POST", booking(list, gifts[0]), friend, "") }
			if hideFirst {
				go hideGift()
				f.waitForLocks(t, "advisory", "", 1)
				go reserveGift()
				f.waitForLocks(t, "", "WHERE share_token = $1 FOR UPDATE", 1)
			} else {
				go reserveGift()
				f.waitForLocks(t, "advisory", "", 1)
				go hideGift()
				f.waitForLocks(t, "", "WHERE id = $1 AND owner_id = $2 FOR UPDATE", 1)
			}
			must(t, blocker.Commit(f.ctx))
			status(t, <-hide, 204)
			if hideFirst {
				status(t, <-reserve, 409)
			} else {
				status(t, <-reserve, 204)
			}
			view := decode[wishlists.View](t, status(t, f.call("GET", shared(list), friend, ""), 200))
			if hideFirst && len(view.Gifts) != 0 || !hideFirst &&
				(len(view.Gifts) != 1 || view.Gifts[0].Active || view.Gifts[0].Reservation != "mine") {
				t.Fatalf("invalid guest view after hide/reserve: %+v", view.Gifts)
			}
		})
	}
}

func TestPostgresArchiveRetriesAfterRollback(t *testing.T) {
	f := setup(t)
	owner, friend := f.register(t, "owner"), f.register(t, "friend")
	list, gifts := f.newList(t, owner, `{"title":"A gift"}`)
	status(t, f.call("POST", booking(list, gifts[0]), friend, ""), 204)
	f.setClose(t, list.ID, "-1 hour")
	_, err := f.db.Exec(f.ctx, `
		CREATE FUNCTION fail_archive() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'test failure';
		END
		$$;
		CREATE TRIGGER fail_archive BEFORE INSERT ON gift_history
		FOR EACH ROW EXECUTE FUNCTION fail_archive()
	`)
	must(t, err)

	store := wishlists.Store{DB: f.db}
	count, err := store.ArchiveDue(f.ctx)
	if count != 0 || err == nil || !strings.Contains(err.Error(), "test failure") {
		t.Fatalf("expected an archive failure, got count=%d err=%v", count, err)
	}
	var unchanged bool
	must(t, f.db.QueryRow(f.ctx, `
		SELECT w.archived_at IS NULL AND g.visible_in_archive IS NULL AND g.gifted IS NULL
		       AND EXISTS (SELECT 1 FROM reservations r WHERE r.gift_id = g.id)
		       AND NOT EXISTS (SELECT 1 FROM gift_history h WHERE h.gift_id = g.id)
		FROM gifts g JOIN wishlists w ON w.id = g.wishlist_id WHERE g.id = $1
	`, gifts[0].ID).Scan(&unchanged))
	if !unchanged {
		t.Fatal("failed archiving left partial state")
	}
	_, err = f.db.Exec(f.ctx, "DROP TRIGGER fail_archive ON gift_history")
	must(t, err)
	count, err = store.ArchiveDue(f.ctx)
	must(t, err)
	if count != 1 {
		t.Fatalf("expected the next run to archive the list, got %d", count)
	}
}
