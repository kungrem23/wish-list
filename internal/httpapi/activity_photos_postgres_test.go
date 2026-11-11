package httpapi_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"wishlist/internal/history"
	"wishlist/internal/httpapi"
	"wishlist/internal/photos"
	"wishlist/internal/platform"
	"wishlist/internal/wishlists"
)

func TestPostgresActivityGiftPhotos(t *testing.T) {
	for _, viaSanta := range []bool{false, true} {
		t.Run(fmt.Sprintf("via_santa=%t", viaSanta), func(t *testing.T) {
			f := setup(t)
			owner, friend, stranger := f.register(t, "Owner"), f.register(t, "Friend"), f.register(t, "Stranger")
			list, gifts := f.newList(t, owner, `{"title":"Gift with photos"}`, `{"title":"No photos"}`)
			ids := []string{platform.Token(), platform.Token()}
			_, err := f.db.Exec(f.ctx, "UPDATE gifts SET photo_ids = $2 WHERE id = $1", gifts[0].ID, ids)
			must(t, err)

			picture := testPNG(t, 2)
			storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || (r.URL.Path != "/test/gift-photos/"+ids[0] && r.URL.Path != "/test/gift-photos/"+ids[1]) {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write(picture)
			}))
			t.Cleanup(storage.Close)
			t.Setenv("S3_ENDPOINT", storage.URL)
			t.Setenv("S3_BUCKET", "test")
			t.Setenv("S3_ACCESS_KEY_ID", "test")
			t.Setenv("S3_SECRET_ACCESS_KEY", "test")
			photoStore, err := photos.FromEnv()
			must(t, err)
			f.handler = httpapi.HandlerWithPhotos(f.db, false, netip.Addr{}, photoStore)

			type photo struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			}
			type activity struct {
				GiftID     string  `json:"gift_id"`
				ShareToken string  `json:"share_token"`
				Photos     []photo `json:"photos"`
			}
			photoPath := fmt.Sprintf("/activity/gifts/%d/photos/%s", gifts[0].ID, ids[0])
			reserve := func(gift wishlists.Gift) {
				t.Helper()
				status(t, f.call("POST", booking(list, gift), friend, ""), 204)
				if viaSanta {
					_, err := f.db.Exec(f.ctx, "UPDATE reservations SET via_santa = true WHERE gift_id = $1", gift.ID)
					must(t, err)
				}
			}
			get := func(path string, session *http.Cookie, code int) {
				t.Helper()
				w := status(t, f.call("GET", path, session, ""), code)
				if code == 200 && (w.Header().Get("Content-Type") != "image/png" ||
					w.Header().Get("Cache-Control") != "no-store" || !bytes.Equal(w.Body.Bytes(), picture)) {
					t.Fatal("activity photo did not return the original image without caching")
				}
			}
			checkEntries := func(path string) []photo {
				t.Helper()
				entries := decode[[]activity](t, status(t, f.call("GET", path, friend, ""), 200))
				if len(entries) != 2 {
					t.Fatalf("expected two activities, got %+v", entries)
				}
				var result []photo
				for _, entry := range entries {
					if entry.GiftID != fmt.Sprint(gifts[0].ID) {
						if len(entry.Photos) != 0 {
							t.Fatal("gift without photos acquired another gift's photos")
						}
						continue
					}
					if len(entry.Photos) != len(ids) || entry.Photos[0].ID != ids[0] || entry.Photos[1].ID != ids[1] {
						t.Fatalf("activity lost or reordered photos: %+v", entry)
					}
					if viaSanta && entry.ShareToken != "" {
						t.Fatal("Santa activity exposed a wishlist share token")
					}
					result = entry.Photos
					for _, p := range result {
						get(strings.TrimPrefix(p.URL, "/api/v1"), friend, 200)
					}
				}
				if len(result) == 0 {
					t.Fatal("gift with photos missing from activity")
				}
				return result
			}

			get(photoPath, friend, 404)
			get(photoPath, nil, 401)
			for _, gift := range gifts {
				reserve(gift)
			}
			original := checkEntries("/reservations")
			get(photoPath, stranger, 404)
			get(photoPath, owner, 404)
			get(fmt.Sprintf("/activity/gifts/%d/photos/%s", gifts[1].ID, ids[0]), friend, 404)
			get(fmt.Sprintf("/activity/gifts/%d/photos/%s", gifts[0].ID, platform.Token()), friend, 404)
			get(fmt.Sprintf("/activity/gifts/%d/photos/invalid", gifts[0].ID), friend, 404)

			status(t, f.call("DELETE", fmt.Sprintf("/reservations/%d", gifts[0].ID), friend, ""), 204)
			get(photoPath, friend, 404)
			reserve(gifts[0])
			status(t, f.call("DELETE", giftPath(list, gifts[0]), owner, ""), 204)
			checkEntries("/reservations")
			f.setClose(t, list.ID, "-1 second")
			checkEntries("/reservations")
			count, err := (wishlists.Store{DB: f.db}).ArchiveDue(f.ctx)
			must(t, err)
			if count != 1 {
				t.Fatalf("expected one archived wishlist, got %d", count)
			}
			archived := checkEntries("/history")
			if original[0].URL != archived[0].URL {
				t.Fatal("photo URL changed when reservation moved to history")
			}
			get(photoPath, stranger, 404)
			status(t, f.call("DELETE", fmt.Sprintf("/history/%d", gifts[0].ID), friend, ""), 204)
			get(photoPath, friend, 404)
			entries := decode[[]history.Entry](t, status(t, f.call("GET", "/history", friend, ""), 200))
			if len(entries) != 1 || entries[0].GiftID != gifts[1].ID {
				t.Fatal("history deletion changed the wrong entry")
			}
		})
	}
}
