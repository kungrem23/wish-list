package httpapi_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"wishlist/internal/httpapi"
	"wishlist/internal/photos"
	"wishlist/internal/platform"
	"wishlist/internal/santa"
	"wishlist/internal/wishlists"
)

type santaFixture struct {
	fixture
	t         *testing.T
	event     santa.Created
	organizer *http.Cookie
}

func newSantaEvent(t *testing.T, f fixture, zone string, days int, participate bool) santaFixture {
	t.Helper()
	organizer := f.register(t, "Organizer")
	var now time.Time
	must(t, f.db.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now))
	loc, err := time.LoadLocation(zone)
	must(t, err)
	date := now.In(loc).AddDate(0, 0, days).Format(time.DateOnly)
	created := decode[santa.Created](t, status(t, f.call("POST", "/santa/events", organizer, fmt.Sprintf(
		`{"title":"Holiday exchange","exchange_date":%q,"time_zone":%q,"budget":"2000","currency":"RUB","rules":"Meet at noon","participate":%t}`,
		date, zone, participate)), 201))
	return santaFixture{f, t, created, organizer}
}

func (f santaFixture) path() string { return fmt.Sprintf("/santa/events/%d", f.event.ID) }

func (f santaFixture) invite() string { return "/santa/invitations/" + f.event.Token }

func (f santaFixture) view(session *http.Cookie) santa.View {
	f.t.Helper()
	return decode[santa.View](f.t, status(f.t, f.call("GET", f.path(), session, ""), 200))
}

func (f santaFixture) apply(session *http.Cookie) {
	f.t.Helper()
	status(f.t, f.call("POST", f.invite()+"/applications", session, ""), 200)
}

func (f santaFixture) decision(view santa.View, name, decision string) (string, string) {
	f.t.Helper()
	for _, app := range view.Applications {
		if app.Name == name {
			return fmt.Sprintf("%s/applications/%d", f.path(), app.UserID),
				fmt.Sprintf(`{"status":%q,"version":"%d"}`, decision, view.Version)
		}
	}
	f.t.Fatalf("no application for %s", name)
	return "", ""
}

func (f santaFixture) decide(name, decision string) {
	f.t.Helper()
	path, body := f.decision(f.view(f.organizer), name, decision)
	status(f.t, f.call("PUT", path, f.organizer, body), 204)
}

func (f santaFixture) grant(round int, listID int64) string {
	return fmt.Sprintf("%s/rounds/%d/wishlists/%d", f.path(), round, listID)
}

func (f santaFixture) bind(who *http.Cookie, listID int64, want int) {
	f.t.Helper()
	status(f.t, f.call("PUT", f.path()+"/wishlist", who, fmt.Sprintf(`{"wishlist_id":"%d"}`, listID)), want)
}

func (f santaFixture) draw(body string) *httptest.ResponseRecorder {
	return f.call("POST", f.path()+"/draw", f.organizer, body)
}

func drawBody(view santa.View, names ...string) string {
	members := make([]string, 0, len(names))
	for _, app := range view.Applications {
		for _, name := range names {
			if app.Name == name {
				members = append(members, fmt.Sprintf("%q", fmt.Sprint(app.UserID)))
			}
		}
	}
	return fmt.Sprintf(`{"version":"%d","members":[%s]}`, view.Version, strings.Join(members, ","))
}

func (f santaFixture) firstDraw() {
	f.t.Helper()
	status(f.t, f.draw(drawBody(f.view(f.organizer))), 204)
}

func TestPostgresSantaApplicationsAndPrivateWishlist(t *testing.T) {
	f := newSantaEvent(t, setup(t), "Asia/Kathmandu", 4, false)
	alex, bella, chris := f.register(t, "Alex"), f.register(t, "Bella"), f.register(t, "Chris")
	status(t, f.call("GET", f.invite(), nil, ""), 401)
	status(t, f.call("GET", f.path(), alex, ""), 404)
	status(t, f.call("POST", f.path()+"/invitation", alex, ""), 404)
	status(t, f.call("POST", f.path()+"/draw", alex, `{"version":"1"}`), 404)
	oldInvite := f.invite()
	link := decode[santa.InvitationLink](t, status(t, f.call("POST", f.path()+"/invitation", f.organizer, ""), 200))
	f.event.Token = link.Token
	status(t, f.call("GET", oldInvite, alex, ""), 404)
	status(t, f.call("POST", oldInvite+"/applications", alex, ""), 404)
	var hash []byte
	must(t, f.db.QueryRow(f.ctx, "SELECT invite_hash FROM santa_events WHERE id = $1", f.event.ID).Scan(&hash))
	wantHash := sha256.Sum256([]byte(link.Token))
	if !bytes.Equal(hash, wantHash[:]) {
		t.Fatal("invitation is not stored as its hash")
	}

	f.apply(alex)
	applied := f.view(f.organizer)
	f.apply(alex)
	if duplicate := f.view(f.organizer); duplicate.Version != applied.Version || len(duplicate.Applications) != 1 {
		t.Fatal("retry changed the application")
	}
	f.apply(bella)
	f.apply(chris)
	f.decide("Chris", "rejected")
	if view := f.view(chris); view.Status != "rejected" || len(view.Applications) != 0 {
		t.Fatalf("unexpected rejected application view: %+v", view)
	}
	path, body := f.decision(f.view(f.organizer), "Alex", "approved")
	status(t, f.call("PUT", path, alex, body), 404)
	status(t, f.call("PUT", path, f.organizer, body), 204)
	status(t, f.draw(drawBody(f.view(f.organizer), "Bella")), 400)
	f.decide("Bella", "approved")

	event := f.view(f.organizer)
	created := status(t, f.call("POST", f.path()+"/wishlist", bella, fmt.Sprintf(
		`{"title":"Bella's wishes","holiday_date":%q,"time_zone":%q}`, event.ExchangeDate, event.TimeZone)), 201)
	list := decode[wishlists.Summary](t, created)
	if strings.Contains(created.Body.String(), `"share_token"`) {
		t.Fatal("event creation route leaked the ordinary wishlist token")
	}
	if list.HolidayDate != event.ExchangeDate || list.TimeZone != event.TimeZone || !list.CloseAt.Equal(event.CloseAt) {
		t.Fatalf("list did not follow ordinary creation rules: %+v", list)
	}
	status(t, f.call("POST", f.path()+"/wishlist", bella, f.details(t, 0, "UTC")), 400)
	f.bind(alex, list.ID, 404)
	status(t, f.call("POST", ownerPath(list.ID)+"/gifts", bella, `{"title":"A book"}`), 204)
	gift := decode[wishlists.View](t, status(t, f.call("GET", ownerPath(list.ID), bella, ""), 200)).Gifts[0]

	var imageBytes bytes.Buffer
	must(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	photoID := platform.Token()
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/santa-test/gift-photos/"+photoID {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBytes.Bytes())
	}))
	t.Cleanup(storage.Close)
	t.Setenv("S3_ENDPOINT", storage.URL)
	t.Setenv("S3_BUCKET", "santa-test")
	t.Setenv("S3_ACCESS_KEY_ID", "local-test")
	t.Setenv("S3_SECRET_ACCESS_KEY", "local-test")
	photoStore, err := photos.FromEnv()
	must(t, err)
	f.handler = httpapi.HandlerWithPhotos(f.db, false, netip.Addr{}, photoStore)
	_, err = f.db.Exec(f.ctx, "UPDATE gifts SET photo_ids = ARRAY[$2::text] WHERE id = $1", gift.ID, photoID)
	must(t, err)

	grant := f.grant(1, list.ID)
	status(t, f.call("GET", grant, alex, ""), 404)
	f.firstDraw()
	if view := f.view(alex); view.Recipient == nil || view.Recipient.Name != "Bella" ||
		view.Recipient.WishlistID == nil || *view.Recipient.WishlistID != list.ID || len(view.Applications) > 0 {
		t.Fatalf("invalid participant view: %+v", view)
	}
	if view := f.view(f.organizer); view.Recipient != nil || len(view.Applications) != 3 {
		t.Fatalf("organizer saw assignment data: %+v", view)
	}
	if view := f.view(chris); view.Recipient != nil {
		t.Fatalf("rejected applicant saw an assignment: %+v", view)
	}
	status(t, f.call("PUT", giftPath(list, gift), bella, `{"title":"Updated gift"}`), 204)
	view := status(t, f.call("GET", grant, alex, ""), 200)
	photoPath := fmt.Sprintf("%s/gifts/%d/photos/%s", grant, gift.ID, photoID)
	if response := view.Body.String(); strings.Contains(response, "share_token") ||
		!strings.Contains(response, "/api/v1"+photoPath) || !strings.Contains(response, "Updated gift") {
		t.Fatal(response)
	}
	imageResponse := status(t, f.call("GET", photoPath, alex, ""), 200)
	if !bytes.Equal(imageResponse.Body.Bytes(), imageBytes.Bytes()) || imageResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("photo response was incorrect or cacheable")
	}
	for _, session := range []*http.Cookie{f.organizer, bella, chris} {
		status(t, f.call("GET", grant, session, ""), 404)
		status(t, f.call("GET", photoPath, session, ""), 404)
	}
	status(t, f.call("GET", photoPath, nil, ""), 401)
	status(t, f.call("PUT", f.path()+"/wishlist", bella, `{"wishlist_id":null}`), 204)
	status(t, f.call("GET", grant, alex, ""), 404)
	status(t, f.call("GET", photoPath, alex, ""), 404)
	f.bind(bella, list.ID, 204)
	status(t, f.call("GET", photoPath, alex, ""), 200)

	status(t, f.draw(drawBody(f.view(f.organizer), "Alex", "Bella")), 204)
	status(t, f.call("GET", grant, alex, ""), 404)
	status(t, f.call("GET", photoPath, alex, ""), 404)
	status(t, f.call("GET", f.grant(2, list.ID), alex, ""), 200)
	currentGrant := f.grant(2, list.ID)
	currentPhoto := fmt.Sprintf("%s/gifts/%d/photos/%s", currentGrant, gift.ID, photoID)
	currentReservation := fmt.Sprintf("%s/gifts/%d/reservation", currentGrant, gift.ID)
	status(t, f.call("POST", currentReservation, alex, ""), 204)
	status(t, f.call("DELETE", currentReservation, alex, ""), 204)
	f.setClose(t, list.ID, "-1 second")
	status(t, f.call("GET", currentGrant, alex, ""), 404)
	status(t, f.call("GET", currentPhoto, alex, ""), 404)
	status(t, f.call("POST", currentReservation, alex, ""), 404)
	status(t, f.call("DELETE", currentReservation, alex, ""), 404)
	f.bind(bella, list.ID, 404)
	recipient := f.view(alex).Recipient
	if recipient == nil || recipient.Name != "Bella" || recipient.WishlistID != nil {
		t.Fatalf("closing a wishlist changed the assignment or kept its access: %+v", recipient)
	}
	status(t, f.call("GET", ownerPath(list.ID), bella, ""), 200)
}

func TestPostgresSantaConcurrentDrawAndRollback(t *testing.T) {
	f := newSantaEvent(t, setup(t), "UTC", 3, true)
	alex, bella := f.register(t, "Alex"), f.register(t, "Bella")
	f.apply(alex)
	f.apply(bella)
	f.decide("Alex", "approved")
	view := f.view(f.organizer)
	path, body := f.decision(view, "Bella", "approved")

	race := func(first, second func() *httptest.ResponseRecorder) {
		t.Helper()
		start := make(chan struct{})
		results := make(chan *httptest.ResponseRecorder, 2)
		for _, call := range []func() *httptest.ResponseRecorder{first, second} {
			go func() { <-start; results <- call() }()
		}
		close(start)
		a, b := <-results, <-results
		if a.Code != 204 {
			a, b = b, a
		}
		status(t, a, 204)
		status(t, b, 409)
	}

	race(func() *httptest.ResponseRecorder { return f.call("PUT", path, f.organizer, body) },
		func() *httptest.ResponseRecorder { return f.draw(drawBody(view)) })
	if f.view(bella).Status == "approved" {
		f.decide("Bella", "removed")
	}
	view = f.view(f.organizer)
	draw := drawBody(view)
	round := view.Round
	race(func() *httptest.ResponseRecorder { return f.draw(draw) },
		func() *httptest.ResponseRecorder { return f.draw(draw) })
	status(t, f.draw(draw), 409)
	view = f.view(f.organizer)
	if view.Round != round+1 {
		t.Fatalf("concurrent draws advanced the round more than once: %+v", view)
	}
	previousBella := f.view(bella).Status

	list, _ := f.newList(t, f.organizer, `{"title":"Current gift"}`)
	f.bind(f.organizer, list.ID, 204)
	oldRound := view.Round
	previous := f.view(alex)
	if previous.Recipient == nil {
		t.Fatal("missing Alex's assignment")
	}
	redraw := drawBody(view)
	_, err := f.db.Exec(f.ctx, `
		CREATE FUNCTION fail_draw() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'test draw failure';
		END
		$$;
		CREATE TRIGGER fail_draw BEFORE INSERT ON santa_assignments
		FOR EACH ROW EXECUTE FUNCTION fail_draw()
	`)
	must(t, err)
	status(t, f.draw(redraw), 500)
	after := f.view(alex)
	if after.Round != oldRound || after.Version != view.Version || after.Recipient == nil ||
		after.Recipient.Name != previous.Recipient.Name || after.Recipient.WishlistID == nil ||
		*after.Recipient.WishlistID != list.ID {
		t.Fatalf("failed draw changed the old round: %+v", after)
	}
	status(t, f.call("GET", f.grant(oldRound, list.ID), alex, ""), 200)
	if f.view(bella).Status != previousBella {
		t.Fatal("failed draw changed Bella's application")
	}
	_, err = f.db.Exec(f.ctx, "DROP TRIGGER fail_draw ON santa_assignments")
	must(t, err)
	status(t, f.draw(redraw), 204)
	if after := f.view(alex); after.Round != oldRound+1 || after.Recipient == nil || after.Status != "approved" {
		t.Fatalf("redraw did not retain approved participant: %+v", after)
	}
	if after := f.view(f.organizer); after.Recipient == nil || after.Recipient.Name != "Alex" {
		t.Fatalf("participating organizer should see only their own result: %+v", after)
	}
	status(t, f.call("GET", f.grant(oldRound, list.ID), alex, ""), 404)
	status(t, f.call("GET", f.grant(oldRound+1, list.ID), alex, ""), 200)
}

func TestPostgresSantaDeadline(t *testing.T) {
	f := newSantaEvent(t, setup(t), "Asia/Kathmandu", 0, true)
	alex := f.register(t, "Alex")
	event := f.view(f.organizer)
	loc, err := time.LoadLocation(event.TimeZone)
	must(t, err)
	date, err := time.ParseInLocation(time.DateOnly, event.ExchangeDate, loc)
	must(t, err)
	if event.Closed || !event.CloseAt.Equal(date.AddDate(0, 0, 1)) {
		t.Fatalf("unexpected local close: %+v", event)
	}
	f.apply(alex)
	f.decide("Alex", "approved")
	f.firstDraw()
	created := status(t, f.call("POST", f.path()+"/wishlist", f.organizer, `{"title":"Still bookable outside Santa"}`), 201)
	listID := decode[wishlists.Summary](t, created).ID
	status(t, f.call("POST", ownerPath(listID)+"/gifts", f.organizer, `{"title":"Gift"}`), 204)
	ownView := decode[wishlists.View](t, status(t, f.call("GET", ownerPath(listID), f.organizer, ""), 200))
	list, gift := ownView.Summary, ownView.Gifts[0]
	if list.CloseAt != nil || list.Phase != "editing" || list.ShareToken == "" {
		t.Fatalf("event creation did not create an undated wishlist: %+v", list)
	}
	grant := f.grant(1, list.ID)
	reservation := fmt.Sprintf("%s/gifts/%d/reservation", grant, gift.ID)
	status(t, f.call("GET", grant, alex, ""), 200)
	status(t, f.call("POST", reservation, alex, ""), 204)
	beforeClose := f.view(f.organizer)
	undrawn := newSantaEventInArchive(t, f.fixture, f.organizer)

	var closeAt time.Time
	must(t, f.db.QueryRow(f.ctx, `
		UPDATE santa_events SET close_at = clock_timestamp() + interval '1.5 seconds' WHERE id = $1 RETURNING close_at
	`, f.event.ID).Scan(&closeAt))
	tx, err := f.db.Begin(f.ctx)
	must(t, err)
	t.Cleanup(func() { _ = tx.Rollback(f.ctx) })
	_, err = tx.Exec(f.ctx, "SELECT id FROM santa_events WHERE id = $1 FOR UPDATE", f.event.ID)
	must(t, err)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- f.draw(drawBody(beforeClose, "Alex", "Organizer")) }()
	f.waitForLocks(t, "", "FROM santa_events WHERE id = $1 FOR UPDATE", 1)
	f.waitUntil(t, closeAt)
	must(t, tx.Commit(f.ctx))
	status(t, <-result, 409)
	status(t, f.call("POST", f.invite()+"/applications", alex, ""), 409)
	f.bind(f.organizer, list.ID, 409)
	status(t, f.call("GET", grant, alex, ""), 404)
	status(t, f.call("POST", reservation, alex, ""), 404)
	status(t, f.call("DELETE", reservation, alex, ""), 404)
	status(t, f.call("DELETE", fmt.Sprintf("/reservations/%d", gift.ID), alex, ""), 204)
	status(t, f.call("POST", booking(list, gift), alex, ""), 204)
	if archived := f.view(alex); !archived.Closed || archived.Recipient == nil ||
		archived.Recipient.Name != "Organizer" || archived.Recipient.WishlistID != nil {
		t.Fatalf("invalid archive view: %+v", archived)
	}
	if !undrawn.Closed || undrawn.Round != 0 || undrawn.Recipient != nil {
		t.Fatalf("invalid undrawn archive view: %+v", undrawn)
	}
}

func newSantaEventInArchive(t *testing.T, f fixture, organizer *http.Cookie) santa.View {
	t.Helper()
	created := decode[santa.Created](t, status(t, f.call("POST", "/santa/events", organizer, fmt.Sprintf(
		`{"title":"Undrawn","exchange_date":%q,"time_zone":"UTC","budget":"0","currency":"RUB"}`,
		time.Now().UTC().AddDate(0, 0, 2).Format(time.DateOnly))), 201))
	_, err := f.db.Exec(f.ctx, "UPDATE santa_events SET close_at = clock_timestamp() - interval '1 second' WHERE id = $1", created.ID)
	must(t, err)
	return (santaFixture{f, t, created, organizer}).view(organizer)
}

func TestPostgresSantaRedrawChecksCurrentBookings(t *testing.T) {
	for _, source := range []string{"santa", "shared"} {
		t.Run(source, func(t *testing.T) {
			f := newSantaEvent(t, setup(t), "UTC", 3, false)
			alex, bella, chris := f.register(t, "Alex"), f.register(t, "Bella"), f.register(t, "Chris")
			f.apply(alex)
			f.apply(bella)
			f.decide("Alex", "approved")
			f.decide("Bella", "approved")
			f.firstDraw()
			f.apply(chris)
			list, gifts := f.newList(t, bella, `{"title":"Assigned gift"}`)
			otherList, otherGifts := f.newList(t, chris, `{"title":"Unrelated booking"}`)
			f.bind(bella, list.ID, 204)
			grant := f.grant(1, list.ID)
			santaBooking := fmt.Sprintf("%s/gifts/%d/reservation", grant, gifts[0].ID)

			status(t, f.call("POST", booking(otherList, otherGifts[0]), alex, ""), 204)
			redraw := drawBody(f.view(f.organizer), "Alex", "Chris")
			// The organizer has already opened the event when this booking arrives.
			var conflict *httptest.ResponseRecorder
			if source == "santa" {
				status(t, f.call("POST", santaBooking, alex, ""), 204)
				status(t, f.call("POST", booking(list, gifts[0]), chris, ""), 409)
				conflict = f.draw(redraw)
			} else {
				blocker := f.pauseWrites(t, "INSERT", "reservations")
				reserved := make(chan *httptest.ResponseRecorder, 1)
				go func() { reserved <- f.call("POST", booking(list, gifts[0]), alex, "") }()
				f.waitForLocks(t, "advisory", "", 1)
				drawing := make(chan *httptest.ResponseRecorder, 1)
				go func() { drawing <- f.draw(redraw) }()
				f.waitForLocks(t, "", "FOR NO KEY UPDATE OF u", 1)
				must(t, blocker.Commit(f.ctx))
				status(t, <-reserved, 204)
				conflict = <-drawing
				status(t, f.call("POST", santaBooking, alex, ""), 409)
			}
			warning := decode[map[string]any](t, status(t, conflict, 409))
			if len(warning) != 2 || warning["confirm_round"] != float64(1) || warning["error"] != santa.ConfirmationMessage {
				t.Fatalf("unexpected warning payload: %+v", warning)
			}
			status(t, f.call("GET", grant, alex, ""), 200)
			status(t, f.draw(strings.TrimSuffix(redraw, "}")+`,"confirm_round":2}`), 409)
			confirmed := strings.TrimSuffix(redraw, "}") + `,"confirm_round":1}`
			status(t, f.draw(confirmed), 204)
			status(t, f.draw(confirmed), 409)
			status(t, f.call("GET", grant, alex, ""), 404)
			status(t, f.call("GET", f.grant(2, list.ID), alex, ""), 200)

			bookings := decode[[]wishlists.Booking](t, status(t, f.call("GET", "/reservations", alex, ""), 200))
			if len(bookings) != 2 || bookings[0].GiftID != gifts[0].ID || !bookings[0].CanCancel ||
				(source == "santa" && bookings[0].ShareToken != "") || (source == "shared" && bookings[0].ShareToken != list.ShareToken) {
				t.Fatalf("booking was lost or exposed the wrong access: %+v", bookings)
			}
			cancel := fmt.Sprintf("/reservations/%d", gifts[0].ID)
			status(t, f.call("DELETE", cancel, chris, ""), 404)
			status(t, f.call("POST", booking(list, gifts[0]), chris, ""), 409)
			status(t, f.call("DELETE", cancel, alex, ""), 204)
			status(t, f.call("POST", booking(list, gifts[0]), chris, ""), 204)
		})
	}
}

func TestPostgresSantaWarningUsesOnlyLiveBookingsForCurrentPair(t *testing.T) {
	f := newSantaEvent(t, setup(t), "UTC", 3, false)
	alex, bella, chris := f.register(t, "Alex"), f.register(t, "Bella"), f.register(t, "Chris")
	f.apply(alex)
	f.apply(bella)
	f.decide("Alex", "approved")
	f.decide("Bella", "approved")
	f.firstDraw()

	current, gifts := f.newList(t, bella, `{"title":"A gift on an unbound list"}`)
	past, pastGifts := f.newList(t, bella, `{"title":"A gift going to history"}`)
	unrelated, unrelatedGifts := f.newList(t, chris, `{"title":"A gift for someone else"}`)
	status(t, f.call("POST", booking(unrelated, unrelatedGifts[0]), alex, ""), 204)
	status(t, f.call("POST", booking(past, pastGifts[0]), alex, ""), 204)
	f.setClose(t, past.ID, "-1 minute")
	_, err := (wishlists.Store{DB: f.db}).ArchiveDue(f.ctx)
	must(t, err)
	status(t, f.call("POST", booking(current, gifts[0]), alex, ""), 204)

	redraw := drawBody(f.view(f.organizer), "Alex", "Bella")
	warning := decode[map[string]any](t, status(t, f.draw(redraw), 409))
	if warning["confirm_round"] != float64(1) {
		t.Fatal("booking for the same recipient on an unbound list did not trigger a warning")
	}
	status(t, f.call("DELETE", booking(current, gifts[0]), alex, ""), 204)
	status(t, f.draw(redraw), 204)
}

func TestPostgresSantaOrganizerCannotRemoveSelf(t *testing.T) {
	f := newSantaEvent(t, setup(t), "UTC", 3, true)
	alex := f.register(t, "Alex")
	f.apply(alex)
	f.decide("Alex", "approved")

	for _, stage := range []string{"before_draw", "after_draw"} {
		t.Run(stage, func(t *testing.T) {
			before := f.view(f.organizer)
			path, body := f.decision(before, "Organizer", "removed")
			status(t, f.call("PUT", path, f.organizer, body), 403)
			after := f.view(f.organizer)
			if after.Status != "approved" || after.Version != before.Version || after.Round != before.Round {
				t.Fatalf("self-removal changed the event: %+v", after)
			}
			if before.Recipient != nil && (after.Recipient == nil || after.Recipient.Name != before.Recipient.Name) {
				t.Fatalf("self-removal changed the organizer's assignment: %+v", after.Recipient)
			}
		})
		if stage == "before_draw" {
			f.firstDraw()
		}
	}

	f.decide("Alex", "removed")
	if f.view(alex).Status != "removed" || f.view(f.organizer).Status != "approved" {
		t.Fatal("organizer could not remove another participant")
	}
}

func TestPostgresSantaDrawIncludesAllApprovedAndExpelsSeparately(t *testing.T) {
	f := newSantaEvent(t, setup(t), "UTC", 3, false)
	people := map[string]*http.Cookie{
		"Alex": f.register(t, "Alex"), "Bella": f.register(t, "Bella"), "Chris": f.register(t, "Chris"),
	}
	lists := map[string]wishlists.Summary{}
	gifts := map[string]wishlists.Gift{}
	for _, name := range []string{"Alex", "Bella", "Chris"} {
		session := people[name]
		f.apply(session)
		list, cards := f.newList(t, session, `{"title":"A gift"}`)
		lists[name], gifts[name] = list, cards[0]
	}
	f.decide("Alex", "approved")
	f.decide("Bella", "approved")
	f.bind(people["Alex"], lists["Alex"].ID, 204)
	f.bind(people["Bella"], lists["Bella"].ID, 204)
	f.firstDraw()

	f.decide("Chris", "approved")
	f.bind(people["Chris"], lists["Chris"].ID, 204)
	view := f.view(people["Chris"])
	if !view.RosterChanged || view.Recipient != nil || view.Status != "approved" {
		t.Fatalf("incorrect view for an approved participant awaiting the next draw: %+v", view)
	}
	// An old client can still send a subset, but the draw always uses the stored roster.
	status(t, f.draw(drawBody(f.view(f.organizer), "Alex", "Bella")), 204)
	if f.view(f.organizer).RosterChanged {
		t.Fatal("draw did not restore a complete roster")
	}

	var fromBella, toBella, unaffected string
	for name, session := range people {
		view := f.view(session)
		if view.Recipient == nil || view.Recipient.Name == name || view.Recipient.WishlistID == nil {
			t.Fatalf("%s did not receive a valid assignment: %+v", name, view.Recipient)
		}
		recipient := view.Recipient.Name
		grant := f.grant(view.Round, lists[recipient].ID)
		status(t, f.call("GET", grant, session, ""), 200)
		switch {
		case name == "Bella":
			fromBella = recipient
			status(t, f.call("POST", fmt.Sprintf("%s/gifts/%d/reservation", grant, gifts[recipient].ID), session, ""), 204)
		case recipient == "Bella":
			toBella = name
			status(t, f.call("POST", fmt.Sprintf("%s/gifts/%d/reservation", grant, gifts[recipient].ID), session, ""), 204)
		default:
			unaffected = name
		}
	}
	admin := f.view(f.organizer)
	path, body := f.decision(admin, "Bella", "removed")
	status(t, f.call("PUT", path, people["Alex"], body), 404)
	status(t, f.call("PUT", path, f.organizer, body), 204)
	if view := f.view(people["Bella"]); view.Status != "removed" || view.Recipient != nil || view.Round != admin.Round {
		t.Fatalf("participant retained access after removal: %+v", view)
	}
	if view := f.view(people[toBella]); !view.RosterChanged || view.Recipient != nil {
		t.Fatalf("giver retained the removed recipient: %+v", view)
	}
	if view := f.view(people[unaffected]); view.Recipient == nil {
		t.Fatal("unrelated assignment was lost before the next draw")
	}
	status(t, f.call("GET", f.grant(admin.Round, lists[fromBella].ID), people["Bella"], ""), 404)
	status(t, f.call("GET", f.grant(admin.Round, lists["Bella"].ID), people[toBella], ""), 404)
	status(t, f.call("GET", f.grant(admin.Round, lists[toBella].ID), people[unaffected], ""), 200)
	status(t, f.call("PUT", f.path()+"/wishlist", people["Bella"], `{"wishlist_id":null}`), 404)
	f.apply(people["Bella"])
	if f.view(people["Bella"]).Status != "removed" {
		t.Fatal("invitation readmitted an excluded participant")
	}
	status(t, f.call("DELETE", fmt.Sprintf("/reservations/%d", gifts[fromBella].ID), people["Bella"], ""), 204)
	status(t, f.call("DELETE", fmt.Sprintf("/reservations/%d", gifts["Bella"].ID), people[toBella], ""), 204)
	status(t, f.draw(drawBody(admin)), 409)
	status(t, f.draw(drawBody(f.view(f.organizer), "Bella")), 204)
	if view := f.view(people["Alex"]); view.Recipient == nil || view.Recipient.Name != "Chris" || view.RosterChanged {
		t.Fatalf("redraw did not use all remaining participants: %+v", view)
	}
	if view := f.view(people["Chris"]); view.Recipient == nil || view.Recipient.Name != "Alex" {
		t.Fatalf("redraw did not include Chris: %+v", view)
	}
	if view := f.view(people["Bella"]); view.Recipient != nil || view.Status != "removed" {
		t.Fatalf("redraw restored an excluded participant: %+v", view)
	}
}
