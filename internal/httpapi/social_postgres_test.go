package httpapi_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"wishlist/internal/accounts"
	"wishlist/internal/wishlists"
)

func TestPostgresFollowsAndDeliveryAddressPrivacy(t *testing.T) {
	f := setup(t)
	owner, guest, stranger := f.register(t, "Owner"), f.register(t, "Guest"), f.register(t, "Stranger")
	id := func(session *http.Cookie) int64 {
		return decode[accounts.Profile](t, status(t, f.call("GET", "/me", session, ""), 200)).ID
	}
	ownerID, guestID := id(owner), id(guest)
	profilePath := fmt.Sprintf("/users/%d", ownerID)
	guestPath := fmt.Sprintf("/users/%d", guestID)
	created := decode[accounts.Address](t, status(t, f.call("POST", "/me/addresses", owner,
		`{"label":"Озон","address":"Первый адрес"}`), 201))
	addressPath := fmt.Sprintf("/me/addresses/%d", created.ID)
	status(t, f.call("PUT", addressPath, guest, `{"label":"Другой","address":"Новый адрес"}`), 404)
	status(t, f.call("DELETE", addressPath, guest, ""), 404)
	updated := decode[accounts.Address](t, status(t, f.call("PUT", addressPath, owner,
		`{"label":"ВБ","address":"Новый адрес\nКвартира 12"}`), 200))
	status(t, f.call("POST", "/me/addresses", owner, `{"label":" ","address":"Без названия"}`), 400)
	status(t, f.call("POST", "/me/addresses", owner, fmt.Sprintf(`{"label":%q,"address":"Адрес"}`, strings.Repeat("я", 81))), 400)
	status(t, f.call("POST", "/me/addresses", owner, fmt.Sprintf(`{"label":"ПВЗ","address":%q}`, strings.Repeat("я", 501))), 400)
	status(t, f.call("PUT", "/me", owner, fmt.Sprintf(`{"description":%q}`, strings.Repeat("я", 1001))), 400)
	status(t, f.call("PUT", "/me", owner, `{"description":"Первая строка\nВторая <b>строка</b>","birth_date":"1999-01-02"}`), 200)

	list, _ := f.newList(t, owner)
	sharedPath := shared(list) + "/profile"
	status(t, f.call("GET", profilePath, nil, ""), 401)
	status(t, f.call("GET", "/me/following", nil, ""), 401)
	status(t, f.call("GET", "/me/followers", nil, ""), 401)
	status(t, f.call("PUT", profilePath+"/follow", owner, ""), 400)
	status(t, f.call("PUT", "/users/99999/follow", guest, ""), 404)
	status(t, f.call("DELETE", "/users/99999/follow", guest, ""), 204)

	assertProfile := func(path string, session *http.Cookie, following bool, lists int, showAddress bool) {
		t.Helper()
		w := status(t, f.call("GET", path, session, ""), 200)
		profile := decode[wishlists.FriendProfile](t, w)
		if strings.Contains(w.Body.String(), `"email"`) || strings.Contains(w.Body.String(), `"birth_date"`) ||
			profile.ID != ownerID || profile.DisplayName != "Owner" || profile.Age == nil || profile.Description == nil ||
			*profile.Description != "Первая строка\nВторая <b>строка</b>" || profile.IsFollowing != following || len(profile.Lists) != lists {
			t.Fatalf("incorrect public profile: %s", w.Body)
		}
		if showAddress && (len(profile.Addresses) != 1 || profile.Addresses[0] != updated) ||
			!showAddress && strings.Contains(w.Body.String(), `"addresses"`) {
			t.Fatalf("incorrect delivery address visibility: %s", w.Body)
		}
	}
	assertConnections := func(path string, session *http.Cookie, ids ...int64) {
		t.Helper()
		w := status(t, f.call("GET", path, session, ""), 200)
		people := decode[[]accounts.Person](t, w)
		if len(people) != len(ids) || strings.Contains(w.Body.String(), `"addresses"`) || strings.Contains(w.Body.String(), `"email"`) {
			t.Fatalf("incorrect connection list: %s", w.Body)
		}
		for i, person := range people {
			if person.ID != ids[i] || person.DisplayName == "" {
				t.Fatalf("incorrect person: %+v", person)
			}
		}
	}

	assertProfile(profilePath, owner, false, 1, true)
	assertProfile(profilePath, stranger, false, 1, false)
	assertProfile(sharedPath, stranger, false, 1, false)
	status(t, f.call("PUT", guestPath+"/follow", owner, ""), 204)
	assertProfile(profilePath, guest, false, 1, false)
	assertProfile(sharedPath, guest, false, 1, false)
	assertConnections("/me/following", guest)
	assertConnections("/me/followers", guest, ownerID)
	status(t, f.call("DELETE", guestPath+"/follow", owner, ""), 204)

	for range 2 {
		status(t, f.call("PUT", profilePath+"/follow", guest, ""), 204)
	}
	assertProfile(profilePath, guest, true, 1, false)
	assertProfile(sharedPath, guest, true, 1, false)
	assertConnections("/me/following", guest, ownerID)
	assertConnections("/me/followers", owner, guestID)
	assertConnections("/me/following", stranger)
	assertConnections("/me/followers", stranger)
	second := decode[wishlists.Summary](t, status(t, f.call("POST", "/wishlists", owner, `{"title":"Новый активный список"}`), 201))
	assertProfile(profilePath, guest, true, 2, false)
	status(t, f.call("PUT", guestPath+"/follow", owner, ""), 204)
	assertProfile(profilePath, guest, true, 2, true)
	assertProfile(sharedPath, guest, true, 2, true)
	assertConnections("/me/followers", guest, ownerID)
	assertConnections("/me/following", owner, guestID)

	status(t, f.call("DELETE", guestPath+"/follow", owner, ""), 204)
	assertProfile(sharedPath, guest, true, 2, false)
	status(t, f.call("PUT", guestPath+"/follow", owner, ""), 204)
	for range 2 {
		status(t, f.call("DELETE", profilePath+"/follow", guest, ""), 204)
	}
	assertProfile(profilePath, guest, false, 2, false)
	assertProfile(sharedPath, guest, false, 2, false)
	assertConnections("/me/followers", owner)
	assertConnections("/me/following", guest)
	status(t, f.call("PUT", profilePath+"/follow", guest, ""), 204)
	status(t, f.call("POST", ownerPath(list.ID)+"/archive", owner, ""), 204)
	status(t, f.call("POST", ownerPath(second.ID)+"/archive", owner, ""), 204)
	status(t, f.call("GET", sharedPath, guest, ""), 404)
	assertProfile(profilePath, guest, true, 0, true)

	for i := 1; i < 10; i++ {
		status(t, f.call("POST", "/me/addresses", owner, `{"label":"ПВЗ","address":"Запасной адрес"}`), 201)
	}
	status(t, f.call("POST", "/me/addresses", owner, `{"label":"ПВЗ","address":"Лишний адрес"}`), 409)
	status(t, f.call("DELETE", addressPath, owner, ""), 204)
	remaining := decode[[]accounts.Address](t, status(t, f.call("GET", "/me/addresses", owner, ""), 200))
	if len(remaining) != 9 || remaining[0].ID == created.ID {
		t.Fatalf("address was not deleted: %+v", remaining)
	}
}
