package httpapi_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"wishlist/internal/history"
	"wishlist/internal/wishlists"
)

func TestPostgresUpgradeKeepsLegacyWishlistData(t *testing.T) {
	f := setupThrough(t, "004_secret_santa.sql")
	_, err := f.db.Exec(f.ctx, `
		INSERT INTO users (email, display_name, password_hash) VALUES ('owner@old.test', 'Owner', 'hash'), ('guest@old.test', 'Guest', 'hash');
		INSERT INTO wishlists (owner_id, title, holiday_date, time_zone, share_token, close_at, archived_at)
		VALUES (1, 'Active', '2028-02-10', 'Europe/Moscow', 'live-token', '2028-02-10 21:00:00+00', NULL),
		       (1, 'Archived', '2026-01-07', 'UTC', 'old-token', '2026-01-08 00:00:00+00', '2026-01-08 02:00:00+00');
		INSERT INTO gift_photo_objects (id) VALUES ('existing-photo');
		INSERT INTO gifts (wishlist_id, title, photo_ids, gifted, visible_in_archive)
		VALUES (1, 'Reserved', ARRAY['existing-photo'], NULL, NULL), (2, 'Gifted', ARRAY['existing-photo'], true, true);
		INSERT INTO reservations (gift_id, user_id, reserved_at, via_santa) VALUES (1, 2, '2026-01-04 01:00:00+00', true);
		INSERT INTO gift_history (gift_id, user_id, reserved_at) VALUES (2, 2, '2026-01-05 01:00:00+00');
	`)
	must(t, err)
	applyMigration(t, f.ctx, f.db, "../../migrations/005_open_wishlists_and_profiles.sql")
	applyMigration(t, f.ctx, f.db, "../../migrations/006_wishlist_avatars.sql")
	var kept bool
	must(t, f.db.QueryRow(f.ctx, `
		SELECT count(*) = 2 AND bool_and(w.avatar_id IS NULL AND g.price IS NULL AND g.priority = 'normal' AND g.photo_ids = ARRAY[p.id])
		AND bool_or(w.holiday_date = '2028-02-10' AND w.time_zone = 'Europe/Moscow' AND w.share_token = 'live-token'
		            AND w.close_at = '2028-02-10 21:00:00+00' AND w.archived_at IS NULL
		            AND r.user_id = 2 AND r.reserved_at = '2026-01-04 01:00:00+00' AND r.via_santa)
		AND bool_or(w.holiday_date = '2026-01-07' AND w.time_zone = 'UTC' AND w.share_token = 'old-token'
		            AND w.close_at = '2026-01-08 00:00:00+00' AND w.archived_at = '2026-01-08 02:00:00+00'
		            AND h.user_id = 2 AND h.reserved_at = '2026-01-05 01:00:00+00' AND g.gifted AND g.visible_in_archive)
		FROM wishlists w JOIN gifts g ON g.wishlist_id = w.id
		JOIN gift_photo_objects p ON p.id = ANY(g.photo_ids)
		LEFT JOIN reservations r ON r.gift_id = g.id LEFT JOIN gift_history h ON h.gift_id = g.id
	`).Scan(&kept))
	if !kept {
		t.Fatal("migration changed existing wishlists, reservations, history or photo references")
	}
	_, err = f.db.Exec(f.ctx, `UPDATE wishlists SET close_at = NULL WHERE id = 1`)
	if err == nil {
		t.Fatal("the database accepted an incomplete wishlist schedule")
	}
}

func TestPostgresUndatedWishlistAndManualArchive(t *testing.T) {
	f := setup(t)
	owner, guest := f.register(t, "Owner"), f.register(t, "Guest")
	details := `{"title":"Список без даты"}`
	list := decode[wishlists.Summary](t, status(t, f.call("POST", "/wishlists", owner, details), 201))
	base := ownerPath(list.ID)
	if list.CloseAt != nil || list.HolidayDate != "" || list.TimeZone != "" || list.Phase != "editing" {
		t.Fatalf("unexpected undated list: %+v", list)
	}
	status(t, f.call("PUT", base, owner, f.details(t, 3, "Europe/Moscow")), 204)
	dated := decode[wishlists.View](t, status(t, f.call("GET", base, owner, ""), 200))
	if dated.CloseAt == nil || dated.TimeZone != "Europe/Moscow" || dated.CloseAt.UTC().Hour() != 21 {
		t.Fatalf("deadline not recalculated: %+v", dated.Summary)
	}
	status(t, f.call("PUT", base, owner, `{"title":"Без даты","holiday_date":null,"time_zone":null}`), 204)
	status(t, f.call("POST", base+"/gifts", owner, `{"title":"Книга","price":"001234.5","priority":"high"}`), 204)
	status(t, f.call("POST", base+"/gifts", owner, `{"title":"Чашка","price":"0","priority":"low"}`), 204)
	status(t, f.call("POST", base+"/gifts", owner, `{"title":"Скрытый подарок"}`), 204)
	for _, price := range []string{`"-0.01"`, `"1.001"`, `"10000000000.00"`, `"1,25"`, `"1e2"`, `"NaN"`, `50.2`} {
		status(t, f.call("POST", base+"/gifts", owner, fmt.Sprintf(`{"title":"Invalid","price":%s}`, price)), 400)
	}
	status(t, f.call("POST", base+"/gifts", owner, `{"title":"Invalid","priority":"urgent"}`), 400)
	gifts := decode[wishlists.View](t, status(t, f.call("GET", base, owner, ""), 200)).Gifts
	book, hidden, cup := gifts[0], gifts[1], gifts[2]
	status(t, f.call("DELETE", giftPath(list, hidden), owner, ""), 204)
	status(t, f.call("POST", booking(list, book), guest, ""), 204)
	status(t, f.call("PUT", giftPath(list, cup), owner, `{"title":"Чашка","price":null,"priority":"low"}`), 204)
	bookings := decode[[]wishlists.Booking](t, status(t, f.call("GET", "/reservations", guest, ""), 200))
	if len(bookings) != 1 || !bookings[0].CanCancel {
		t.Fatalf("undated booking could not be cancelled: %+v", bookings)
	}
	status(t, f.call("DELETE", booking(list, book), guest, ""), 204)
	status(t, f.call("POST", booking(list, book), guest, ""), 204)
	if body := status(t, f.call("GET", base, owner, ""), 200).Body.String(); strings.Contains(body, `"reservation"`) || strings.Contains(body, `"gifted"`) {
		t.Fatalf("owner saw a live reservation: %s", body)
	}
	count, err := (wishlists.Store{DB: f.db}).ArchiveDue(f.ctx)
	must(t, err)
	if count != 0 {
		t.Fatalf("automatic archive processed %d undated lists", count)
	}

	status(t, f.call("POST", base+"/archive", guest, ""), 404)
	status(t, f.call("POST", base+"/archive", owner, ""), 204)
	status(t, f.call("POST", base+"/archive", owner, ""), 204)
	status(t, f.call("GET", shared(list), guest, ""), 404)
	status(t, f.call("GET", shared(list)+"/profile", guest, ""), 404)
	status(t, f.call("POST", booking(list, cup), guest, ""), 409)
	view := decode[wishlists.View](t, status(t, f.call("GET", base, owner, ""), 200))
	if view.GiftCount != 2 || view.CloseAt != nil || view.Phase != "archived" || !*view.Gifts[0].Gifted || view.Gifts[1].Price != nil {
		t.Fatalf("incorrect archive: %+v, %+v", view.Summary, view.Gifts)
	}
	entries := decode[[]history.Entry](t, status(t, f.call("GET", "/history", guest, ""), 200))
	if len(entries) != 1 || entries[0].GiftID != book.ID || !entries[0].Gifted {
		t.Fatalf("manual archive did not record the booking once: %+v", entries)
	}
	status(t, f.call("PUT", giftPath(list, book)+"/outcome", owner, `{"gifted":false}`), 204)
	for _, input := range []string{details, f.details(t, 4, "Asia/Kathmandu")} {
		copied := decode[wishlists.Summary](t, status(t, f.call("POST", base+"/copy", owner, input), 201))
		view := decode[wishlists.View](t, status(t, f.call("GET", ownerPath(copied.ID), owner, ""), 200))
		if copied.GiftCount != 2 || (copied.CloseAt == nil) != (input == details) ||
			len(view.Gifts) != 2 || view.Gifts[0].Price == nil || *view.Gifts[0].Price != "1234.50" ||
			view.Gifts[0].Priority != "high" || view.Gifts[1].Priority != "low" || view.Gifts[1].Price != nil {
			t.Fatalf("copy lost gift data or schedule: %+v, %+v", copied, view.Gifts)
		}
		if input != details {
			status(t, f.call("POST", ownerPath(copied.ID)+"/archive", owner, ""), 204)
		}
	}
}

func TestPostgresManualArchiveSerializesWithBooking(t *testing.T) {
	for _, reserveFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("reserve_first=%t", reserveFirst), func(t *testing.T) {
			f := setup(t)
			owner, guest := f.register(t, "Owner"), f.register(t, "Guest")
			list := decode[wishlists.Summary](t, status(t, f.call("POST", "/wishlists", owner, `{"title":"Open"}`), 201))
			base := ownerPath(list.ID)
			status(t, f.call("POST", base+"/gifts", owner, `{"title":"Gift"}`), 204)
			gift := decode[wishlists.View](t, status(t, f.call("GET", base, owner, ""), 200)).Gifts[0]
			table, operation := "gifts", "UPDATE"
			waitQuery := "WHERE share_token = $1 FOR UPDATE"
			if reserveFirst {
				table, operation = "reservations", "INSERT"
				waitQuery = "WHERE id = $1 AND owner_id = $2 FOR UPDATE"
			}
			blocker := f.pauseWrites(t, operation, table)
			reservation, archive := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
			reserve := func() { reservation <- f.call("POST", booking(list, gift), guest, "") }
			closeList := func() { archive <- f.call("POST", base+"/archive", owner, "") }
			if reserveFirst {
				go reserve()
			} else {
				go closeList()
			}
			f.waitForLocks(t, "advisory", "", 1)
			if reserveFirst {
				go closeList()
			} else {
				go reserve()
			}
			f.waitForLocks(t, "", waitQuery, 1)
			must(t, blocker.Commit(f.ctx))
			status(t, <-archive, 204)
			wantStatus, wantEntries := 409, 0
			if reserveFirst {
				wantStatus, wantEntries = 204, 1
			}
			status(t, <-reservation, wantStatus)
			entries := decode[[]history.Entry](t, status(t, f.call("GET", "/history", guest, ""), 200))
			if len(entries) != wantEntries {
				t.Fatalf("archive ignored lock order: %+v", entries)
			}
		})
	}
}

func TestPostgresGiftSortingAndUndatedCursors(t *testing.T) {
	f := setup(t)
	owner, guest := f.register(t, "Owner"), f.register(t, "Guest")
	list := decode[wishlists.Summary](t, status(t, f.call("POST", "/wishlists", owner, `{"title":"Undated"}`), 201))
	dated, _ := f.newList(t, owner)
	_, err := f.db.Exec(f.ctx, `
		INSERT INTO wishlists (owner_id, title, share_token)
		SELECT w.owner_id, 'Legacy undated', 'undated-' || n FROM wishlists w CROSS JOIN generate_series(1, 52) n WHERE w.id = $1;
	`, list.ID)
	must(t, err)
	_, err = f.db.Exec(f.ctx, `
		INSERT INTO gifts (wishlist_id, title, priority, created_at)
		SELECT $1, 'Яблоко', 'normal', '2026-01-01 00:00:00+00' FROM generate_series(1, 54);
	`, list.ID)
	must(t, err)
	_, err = f.db.Exec(f.ctx, `
		INSERT INTO gifts (wishlist_id, title, priority, created_at) VALUES
		($1, 'ёж', 'high', '2026-01-02 00:00:00+00'), ($1, 'Ель', 'low', '2026-01-03 00:00:00+00'),
		($1, 'Альбом', 'high', '2026-01-03 00:00:00+00'), ($1, 'ель', 'low', '2026-01-01 00:00:00+00');
	`, list.ID)
	must(t, err)
	checkLists := func(first, second []wishlists.Summary) {
		t.Helper()
		if len(first) != 50 || len(second) != 4 || first[49].HolidayDate != "" || second[0].HolidayDate != "" ||
			first[49].ID >= second[0].ID || second[3].ID != dated.ID || first[0].GiftCount != 58 {
			t.Fatal("undated cursor or whole-list gift count is incorrect")
		}
	}
	first, second := testPages[[]wishlists.Summary](t, f, owner, "/wishlists")
	checkLists(first, second)
	firstProfile, secondProfile := testPages[wishlists.FriendProfile](t, f, guest, shared(list)+"/profile")
	checkLists(firstProfile.Lists, secondProfile.Lists)

	for _, tc := range []struct {
		sort  string
		first []string
		last  string
	}{
		{"priority", []string{"ёж", "Альбом", "Яблоко"}, "ель"},
		{"name", []string{"Альбом", "ёж", "Ель", "ель"}, "Яблоко"},
		{"added", []string{"Альбом", "Ель", "ёж", "ель"}, "Яблоко"},
	} {
		path := shared(list) + "?sort=" + tc.sort
		a, b := testPages[wishlists.View](t, f, guest, path)
		if len(a.Gifts) != 50 || len(b.Gifts) != 8 || b.Gifts[7].Title != tc.last {
			t.Fatalf("%s did not paginate in sort order", tc.sort)
		}
		for i, want := range tc.first {
			if a.Gifts[i].Title != want {
				t.Fatalf("%s position %d: got %q, want %q", tc.sort, i, a.Gifts[i].Title, want)
			}
		}
		ids := map[int64]bool{}
		for _, g := range append(a.Gifts, b.Gifts...) {
			if ids[g.ID] {
				t.Fatalf("sort %s repeated gift %d", tc.sort, g.ID)
			}
			ids[g.ID] = true
		}
		if tc.sort != "priority" {
			cursor := status(t, f.call("GET", path, guest, ""), 200).Header().Get("X-Next-Cursor")
			status(t, f.call("GET", shared(list)+"?sort=priority&after="+cursor, guest, ""), 400)
		}
	}
}
