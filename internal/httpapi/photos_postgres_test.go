package httpapi_test

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"

	"wishlist/internal/accounts"
	"wishlist/internal/httpapi"
	"wishlist/internal/photos"
	"wishlist/internal/wishlists"
)

func setupPhotos(t *testing.T) (fixture, *photos.Store) {
	t.Helper()
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TEST_S3_ENDPOINT and S3 credentials for an isolated test bucket")
	}
	t.Setenv("S3_ENDPOINT", endpoint)
	photoStore, err := photos.FromEnv()
	must(t, err)
	f := setup(t)
	f.handler = httpapi.HandlerWithPhotos(f.db, false, netip.Addr{}, photoStore)
	t.Cleanup(func() {
		rows, err := f.db.Query(f.ctx, "SELECT id FROM gift_photo_objects")
		must(t, err)
		defer rows.Close()
		for rows.Next() {
			var id string
			must(t, rows.Scan(&id))
			must(t, photoStore.Delete(f.ctx, id))
		}
		must(t, rows.Err())
	})

	return f, photoStore
}

func TestPostgresGiftPhotos(t *testing.T) {
	f, photoStore := setupPhotos(t)
	owner := f.register(t, "Owner")
	friend := f.register(t, "Friend")
	list, _ := f.newList(t, owner)
	base := ownerPath(list.ID)
	var pngImage bytes.Buffer
	must(t, png.Encode(&pngImage, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	picture := pngImage.Bytes()

	save := func(method, path string, session *http.Cookie, remove []string, files ...[]byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		must(t, form.WriteField("card", `{"title":"Gift with photos","price":"235.5","priority":"high"}`))
		for _, id := range remove {
			must(t, form.WriteField("remove_photo", id))
		}
		for _, data := range files {
			part, err := form.CreateFormFile("photo", "picture.png")
			must(t, err)
			_, err = part.Write(data)
			must(t, err)
		}
		must(t, form.Close())
		r := httptest.NewRequest(method, "/api/v1"+path, &body).WithContext(f.ctx)
		r.AddCookie(session)
		r.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	type photo struct {
		ID  string
		URL string
	}
	type gift struct {
		wishlists.Gift
		Photos []photo
	}
	view := func(path string, session *http.Cookie) []gift {
		return decode[struct{ Gifts []gift }](t, status(t, f.call("GET", path, session, ""), 200)).Gifts
	}
	get := func(p photo, session *http.Cookie, expected int) {
		t.Helper()
		w := status(t, f.call("GET", strings.TrimPrefix(p.URL, "/api/v1"), session, ""), expected)
		if expected == 200 && (w.Header().Get("Content-Type") != "image/png" || !bytes.Equal(w.Body.Bytes(), picture)) {
			t.Fatal("photo response differs from the uploaded PNG")
		}
	}

	status(t, save("POST", base+"/gifts", friend, nil, picture), 404)
	status(t, save("POST", base+"/gifts", owner, nil, []byte("<svg></svg>")), 400)
	status(t, save("POST", base+"/gifts", owner, nil, make([]byte, 5<<20+1)), 413)
	status(t, save("POST", base+"/gifts", owner, nil, picture, picture), 204)
	gifts := view(base, owner)
	if len(gifts) != 1 || len(gifts[0].Photos) != 2 || gifts[0].Price == nil ||
		*gifts[0].Price != "235.50" || gifts[0].Priority != "high" {
		t.Fatalf("expected one gift with two photos: %+v", gifts)
	}
	card := gifts[0]
	first, second := card.Photos[0], card.Photos[1]
	get(first, owner, 200)
	get(first, friend, 404)
	get(first, nil, 401)
	sharedPhotos := view(shared(list), friend)[0].Photos
	get(sharedPhotos[0], friend, 200)
	path := giftPath(list, card.Gift)
	status(t, save("PUT", path, owner, nil, picture, picture, picture, picture), 400)

	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer unavailable.Close()
	t.Setenv("S3_ENDPOINT", unavailable.URL)
	brokenStore, err := photos.FromEnv()
	must(t, err)
	f.handler = httpapi.HandlerWithPhotos(f.db, false, netip.Addr{}, brokenStore)
	status(t, save("PUT", path, owner, []string{first.ID}, picture), 503)
	f.handler = httpapi.HandlerWithPhotos(f.db, false, netip.Addr{}, photoStore)
	get(first, owner, 200)

	status(t, save("PUT", path, owner, []string{first.ID}), 204)
	get(first, owner, 404)
	status(t, f.call("PUT", path, owner, `{"title":"Updated title"}`), 204)
	get(second, owner, 200)

	store := wishlists.Store{DB: f.db}
	f.setClose(t, list.ID, "-1 second")
	get(sharedPhotos[1], friend, 404)
	status(t, f.call("GET", shared(list), friend, ""), 404)
	_, err = store.ArchiveDue(f.ctx)
	must(t, err)
	get(sharedPhotos[1], friend, 404)
	copyList := decode[wishlists.Summary](t, status(t, f.call("POST", base+"/copy", owner, f.details(t, 4, "UTC")), 201))
	copied := view(ownerPath(copyList.ID), owner)[0]
	if len(copied.Photos) != 1 || copied.Photos[0].ID != second.ID {
		t.Fatalf("copied gift lost its photo: %+v", copied)
	}
	get(copied.Photos[0], owner, 200)
	status(t, save("PUT", giftPath(copyList, copied.Gift), owner, []string{second.ID}), 204)
	_, err = f.db.Exec(f.ctx, "UPDATE gift_photo_objects SET created_at = clock_timestamp() - interval '2 hours'")
	must(t, err)
	_, err = store.RemoveUnusedPhotos(f.ctx, photoStore.Delete)
	must(t, err)
	object, err := photoStore.Get(f.ctx, first.ID)
	if err == nil {
		object.Body.Close()
	}
	if !errors.Is(err, photos.ErrNotFound) {
		t.Fatalf("detached photo still in S3: %v", err)
	}
	get(second, owner, 200)
}

func TestPostgresAvatarReplacementAndPhotoCleanup(t *testing.T) {
	f, photoStore := setupPhotos(t)
	owner, guest := f.register(t, "Owner"), f.register(t, "Guest")
	first, second := testPNG(t, 2), testPNG(t, 3)
	upload := func(data []byte, code int) *httptest.ResponseRecorder {
		t.Helper()
		return status(t, uploadTestAvatar(t, f, owner, "/me/avatar", data), code)
	}
	getAvatar := func(url string, session *http.Cookie, code int, expected []byte) {
		t.Helper()
		w := status(t, f.call("GET", strings.TrimPrefix(url, "/api/v1"), session, ""), code)
		if code == 200 && (w.Header().Get("Cache-Control") != "no-store" || !bytes.Equal(w.Body.Bytes(), expected)) {
			t.Fatal("avatar was cached or did not match the uploaded image")
		}
	}
	initial := decode[accounts.Profile](t, upload(first, 200))
	if initial.AvatarURL == nil {
		t.Fatal("profile missing uploaded avatar")
	}
	getAvatar(*initial.AvatarURL, owner, 200, first)
	getAvatar(*initial.AvatarURL, guest, 200, first)
	getAvatar(*initial.AvatarURL, nil, 401, nil)
	firstID := (*initial.AvatarURL)[strings.LastIndex(*initial.AvatarURL, "/")+1:]
	unsigned, err := http.Get(os.Getenv("TEST_S3_ENDPOINT") + "/" + os.Getenv("S3_BUCKET") + "/gift-photos/" + firstID)
	must(t, err)
	unsigned.Body.Close()
	if unsigned.StatusCode != 403 {
		t.Fatalf("S3 avatar allowed unsigned access: %d", unsigned.StatusCode)
	}
	upload([]byte("invalid image"), 400)

	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(unavailable.Close)
	t.Setenv("S3_ENDPOINT", unavailable.URL)
	brokenStore, err := photos.FromEnv()
	must(t, err)
	f.handler = httpapi.HandlerWithPhotos(f.db, false, netip.Addr{}, brokenStore)
	upload(second, 503)
	f.handler = httpapi.HandlerWithPhotos(f.db, false, netip.Addr{}, photoStore)
	getAvatar(*initial.AvatarURL, owner, 200, first)

	replaced := decode[accounts.Profile](t, upload(second, 200))
	if replaced.AvatarURL == nil || *replaced.AvatarURL == *initial.AvatarURL {
		t.Fatal("avatar replacement did not change the URL")
	}
	getAvatar(*initial.AvatarURL, guest, 404, nil)
	getAvatar(*replaced.AvatarURL, guest, 200, second)
	secondID := (*replaced.AvatarURL)[strings.LastIndex(*replaced.AvatarURL, "/")+1:]
	profilePath, _, _ := strings.Cut(strings.TrimPrefix(*replaced.AvatarURL, "/api/v1"), "/avatar/")
	profile := decode[wishlists.FriendProfile](t, status(t, f.call("GET", profilePath, guest, ""), 200))
	if profile.AvatarURL == nil || *profile.AvatarURL != *replaced.AvatarURL {
		t.Fatal("public profile missing current avatar")
	}
	status(t, f.call("PUT", profilePath+"/follow", guest, ""), 204)
	following := decode[[]accounts.Person](t, status(t, f.call("GET", "/me/following", guest, ""), 200))
	guestProfile := decode[accounts.Profile](t, status(t, f.call("GET", "/me", guest, ""), 200))
	status(t, f.call("PUT", fmt.Sprintf("/users/%d/follow", guestProfile.ID), owner, ""), 204)
	followers := decode[[]accounts.Person](t, status(t, f.call("GET", "/me/followers", guest, ""), 200))
	for _, people := range [][]accounts.Person{following, followers} {
		if len(people) != 1 || people[0].AvatarURL == nil || *people[0].AvatarURL != *replaced.AvatarURL {
			t.Fatal("connections missing the avatar")
		}
	}

	_, err = f.db.Exec(f.ctx, "UPDATE gift_photo_objects SET created_at = clock_timestamp() - interval '2 hours'")
	must(t, err)
	store := wishlists.Store{DB: f.db}
	count, err := store.RemoveUnusedPhotos(f.ctx, photoStore.Delete)
	must(t, err)
	if count != 2 {
		t.Fatalf("expected cleanup to remove the old avatar and failed upload, got %d", count)
	}
	getAvatar(*replaced.AvatarURL, guest, 200, second)
	assertMissing := func(id string) {
		t.Helper()
		obj, err := photoStore.Get(f.ctx, id)
		if err == nil {
			obj.Body.Close()
		}
		if !errors.Is(err, photos.ErrNotFound) {
			t.Fatalf("S3 object was not deleted: %v", err)
		}
	}
	assertMissing(firstID)
	deleted := decode[accounts.Profile](t, status(t, f.call("DELETE", "/me/avatar", owner, ""), 200))
	if deleted.AvatarURL != nil {
		t.Fatal("deleted avatar is still associated with the profile")
	}
	getAvatar(*replaced.AvatarURL, guest, 404, nil)
	count, err = store.RemoveUnusedPhotos(f.ctx, photoStore.Delete)
	must(t, err)
	if count != 1 {
		t.Fatalf("expected cleanup to remove the deleted avatar, got %d", count)
	}
	assertMissing(secondID)
}

func testPNG(t *testing.T, side int) []byte {
	t.Helper()
	var imageBytes bytes.Buffer
	must(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, side, side))))
	return imageBytes.Bytes()
}

func uploadTestAvatar(t *testing.T, f fixture, session *http.Cookie, path string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("photo", "avatar.png")
	must(t, err)
	_, err = part.Write(data)
	must(t, err)
	must(t, form.Close())
	r := httptest.NewRequest("PUT", "/api/v1"+path, &body).WithContext(f.ctx)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.AddCookie(session)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func TestPostgresWishlistAvatarsUseWishlistAccessAndCleanup(t *testing.T) {
	f, store := setupPhotos(t)
	owner, guest := f.register(t, "Owner"), f.register(t, "Guest")
	list, _ := f.newList(t, owner)
	base := ownerPath(list.ID)
	readAvatar := func(path string, session *http.Cookie) string {
		t.Helper()
		out := decode[struct {
			AvatarURL *string `json:"avatar_url"`
		}](t, status(t, f.call("GET", path, session, ""), 200))
		if out.AvatarURL == nil {
			t.Fatalf("missing avatar at %s", path)
		}
		return strings.TrimPrefix(*out.AvatarURL, "/api/v1")
	}
	checkAvatar := func(path string, session *http.Cookie, code int, image []byte) {
		t.Helper()
		w := status(t, f.call("GET", path, session, ""), code)
		if code == 200 && (!bytes.Equal(w.Body.Bytes(), image) || w.Header().Get("Cache-Control") != "no-store") {
			t.Fatal("incorrect avatar response")
		}
	}

	first, second := testPNG(t, 2), testPNG(t, 3)
	status(t, uploadTestAvatar(t, f, guest, base+"/avatar", first), 404)
	status(t, f.call("DELETE", base+"/avatar", guest, ""), 404)
	status(t, uploadTestAvatar(t, f, owner, base+"/avatar", []byte("invalid")), 400)
	status(t, uploadTestAvatar(t, f, owner, base+"/avatar", first), 204)
	initial := readAvatar(base, owner)
	checkAvatar(initial, owner, 200, first)
	checkAvatar(initial, guest, 404, nil)
	checkAvatar(initial, nil, 401, nil)
	checkAvatar(readAvatar(shared(list), guest), guest, 200, first)

	ownerProfile := decode[accounts.Profile](t, status(t, f.call("GET", "/me", owner, ""), 200))
	profile := decode[wishlists.FriendProfile](t, status(t, f.call("GET", fmt.Sprintf("/users/%d", ownerProfile.ID), guest, ""), 200))
	if len(profile.Lists) != 1 || profile.Lists[0].AvatarURL == nil || !strings.HasPrefix(*profile.Lists[0].AvatarURL, "/api/v1"+shared(list)+"/avatar/") {
		t.Fatal("public profile missed the list avatar")
	}
	owned := decode[[]wishlists.Summary](t, status(t, f.call("GET", "/wishlists", owner, ""), 200))
	if len(owned) != 1 || owned[0].AvatarURL == nil || *owned[0].AvatarURL != "/api/v1"+initial {
		t.Fatal("owner list summary missed the avatar")
	}

	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(unavailable.Close)
	t.Setenv("S3_ENDPOINT", unavailable.URL)
	brokenStore, err := photos.FromEnv()
	must(t, err)
	status(t, uploadTestAvatar(t, fixture{ctx: f.ctx, handler: httpapi.HandlerWithPhotos(f.db, false, netip.Addr{}, brokenStore)}, owner, base+"/avatar", second), 503)
	checkAvatar(initial, owner, 200, first)
	status(t, f.call("DELETE", base+"/avatar", owner, ""), 204)
	checkAvatar(initial, owner, 404, nil)
	status(t, uploadTestAvatar(t, f, owner, base+"/avatar", second), 204)
	current := readAvatar(base, owner)
	checkAvatar(current, owner, 200, second)
	sharedCurrent := readAvatar(shared(list), guest)

	event := newSantaEvent(t, f, "UTC", 3, false)
	event.apply(owner)
	event.apply(guest)
	event.decide("Owner", "approved")
	event.decide("Guest", "approved")
	event.bind(owner, list.ID, 204)
	event.firstDraw()
	grant := event.grant(1, list.ID)
	eventAvatar := readAvatar(grant, guest)
	if !strings.HasPrefix(eventAvatar, grant+"/avatar/") || strings.Contains(eventAvatar, list.ShareToken) {
		t.Fatalf("Santa avatar URL used the wrong grant: %s", eventAvatar)
	}
	checkAvatar(eventAvatar, guest, 200, second)
	checkAvatar(eventAvatar, owner, 404, nil)
	event.decide("Guest", "removed")
	checkAvatar(eventAvatar, guest, 404, nil)
	event.decide("Guest", "approved")
	status(t, event.draw(fmt.Sprintf(`{"version":"%d"}`, event.view(event.organizer).Version)), 204)
	eventAvatar = readAvatar(event.grant(2, list.ID), guest)
	checkAvatar(eventAvatar, guest, 200, second)

	f.setClose(t, list.ID, "-1 second")
	checkAvatar(sharedCurrent, guest, 404, nil)
	checkAvatar(eventAvatar, guest, 404, nil)
	status(t, f.call("DELETE", base+"/avatar", owner, ""), 409)
	count, err := (wishlists.Store{DB: f.db}).ArchiveDue(f.ctx)
	must(t, err)
	if count != 1 {
		t.Fatalf("expected one archived wishlist, got %d", count)
	}
	checkAvatar(current, owner, 200, second)
	_, err = f.db.Exec(f.ctx, "UPDATE gift_photo_objects SET created_at = clock_timestamp() - interval '2 hours'")
	must(t, err)
	count, err = (wishlists.Store{DB: f.db}).RemoveUnusedPhotos(f.ctx, store.Delete)
	must(t, err)
	if count != 3 { // Rejected owner check, failed upload, and deleted avatar.
		t.Fatalf("expected 3 abandoned objects, got %d", count)
	}
	initialID := initial[strings.LastIndex(initial, "/")+1:]
	obj, err := store.Get(f.ctx, initialID)
	if err == nil {
		obj.Body.Close()
	}
	if !errors.Is(err, photos.ErrNotFound) {
		t.Fatalf("old wishlist avatar still exists in S3: %v", err)
	}
	checkAvatar(current, owner, 200, second)
}
