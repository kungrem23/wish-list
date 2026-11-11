package httpapi_test

import (
	"strings"
	"testing"
	"time"

	"wishlist/internal/wishlists"
)

func TestPostgresSharedProfilePrivacyAndActiveLists(t *testing.T) {
	f := setup(t)
	owner, friend := f.register(t, "owner"), f.register(t, "friend")
	linked, _ := f.newList(t, owner)
	active, _ := f.newList(t, owner)
	closed, _ := f.newList(t, owner)
	f.newList(t, friend)
	f.setClose(t, closed.ID, "-1 hour")
	path := shared(linked) + "/profile"
	status(t, f.call("GET", path, nil, ""), 401)
	status(t, f.call("GET", "/shared/unknown/profile", friend, ""), 404)
	status(t, f.call("GET", path+"?after=invalid!", friend, ""), 400)

	var today time.Time
	must(t, f.db.QueryRow(f.ctx, `SELECT (statement_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&today))
	for _, tc := range []struct {
		name string
		date string
		age  int
	}{
		{name: "unset", age: -1},
		{name: "future", date: today.AddDate(1, 0, 0).Format(time.DateOnly), age: -1},
		{name: "birthday", date: today.AddDate(-20, 0, 0).Format(time.DateOnly), age: 20},
		{name: "before birthday", date: today.AddDate(-20, 0, 1).Format(time.DateOnly), age: 19},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.db.Exec(f.ctx, `
				UPDATE users SET birth_date = NULLIF($1, '')::date, clothing_size = 'XL', shoe_size_eu = 42.5
				WHERE email = 'owner@example.org'
			`, tc.date)
			must(t, err)
			w := status(t, f.call("GET", path, friend, ""), 200)
			body := w.Body.String()
			if strings.Contains(body, `"birth_date"`) || strings.Contains(body, `"email"`) ||
				strings.Contains(body, "owner@example.org") || (tc.date != "" && strings.Contains(body, tc.date)) {
				t.Fatalf("private profile fields leaked: %s", body)
			}
			profile := decode[wishlists.FriendProfile](t, w)
			if profile.DisplayName != "owner" || profile.Clothing == nil || *profile.Clothing != "XL" ||
				profile.ShoeEU == nil || *profile.ShoeEU != "42.5" {
				t.Fatalf("missing shared profile fields: %s", body)
			}
			if (tc.age < 0) != (profile.Age == nil) || (tc.age >= 0 && *profile.Age != tc.age) {
				t.Fatalf("incorrect age: %s", body)
			}
			if len(profile.Lists) != 2 || profile.Lists[0].ID != linked.ID || profile.Lists[1].ID != active.ID {
				t.Fatalf("incorrect linked or active lists: %s", body)
			}
		})
	}
	status(t, f.call("GET", shared(closed)+"/profile", friend, ""), 404)
	status(t, f.call("GET", shared(closed), friend, ""), 404)
	status(t, f.call("POST", ownerPath(linked.ID)+"/archive", owner, ""), 204)
	status(t, f.call("GET", path, friend, ""), 404)
	status(t, f.call("GET", shared(linked), friend, ""), 404)
}
