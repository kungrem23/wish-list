package httpapi_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"wishlist/internal/accounts"
	"wishlist/internal/httpapi"
	"wishlist/internal/photos"
	"wishlist/internal/platform"
	"wishlist/internal/problem"
	"wishlist/internal/santa"
	"wishlist/internal/wishlists"
)

func TestPostgresPhotoQuotaSerializesConcurrentReservations(t *testing.T) {
	f := setup(t)
	owner := f.register(t, "Owner")
	profile := decode[accounts.Profile](t, status(t, f.call("GET", "/me", owner, ""), 200))
	_, err := f.db.Exec(f.ctx, `INSERT INTO gift_photo_objects (id, owner_id, size_bytes, created_at)
	    SELECT 'existing-' || n, $1, 5242880, clock_timestamp() - interval '2 hours' FROM generate_series(1, 49) n`, profile.ID)
	must(t, err)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			results <- photos.TrackUploads(f.ctx, f.db, profile.ID, []photos.UploadObject{{ID: platform.Token(), Size: 5 << 20}})
		}()
	}
	a, b := <-results, <-results
	if a != nil {
		a, b = b, a
	}
	must(t, a)
	var p *problem.Error
	if !errors.As(b, &p) || p.Status != 409 {
		t.Fatalf("expected one quota rejection, got %v", b)
	}
	var total int64
	must(t, f.db.QueryRow(f.ctx, "SELECT sum(size_bytes) FROM gift_photo_objects").Scan(&total))
	if total != photos.MaxUserBytes {
		t.Fatalf("concurrent uploads bypassed quota: %d", total)
	}
}

func TestPostgresPhotoQuotaMigrationPreservesLegacyAvatar(t *testing.T) {
	f := setupThrough(t, "006_wishlist_avatars.sql")
	owner := f.register(t, "Legacy")
	profile := decode[accounts.Profile](t, status(t, f.call("GET", "/me", owner, ""), 200))
	_, err := f.db.Exec(f.ctx, "INSERT INTO gift_photo_objects (id) VALUES ('legacy-avatar'); UPDATE users SET avatar_id = 'legacy-avatar'")
	must(t, err)
	applyMigration(t, f.ctx, f.db, "../../migrations/007_server_limits.sql")
	var ownerID, size int64
	must(t, f.db.QueryRow(f.ctx, "SELECT owner_id, size_bytes FROM gift_photo_objects WHERE id = 'legacy-avatar'").Scan(&ownerID, &size))
	if ownerID != profile.ID || size != 5<<20 {
		t.Fatal("legacy photo was not conservatively accounted")
	}
	updated := decode[accounts.Profile](t, status(t, f.call("GET", "/me", owner, ""), 200))
	if updated.AvatarURL == nil || *updated.AvatarURL != "/api/v1/users/"+fmt.Sprint(profile.ID)+"/avatar/legacy-avatar" {
		t.Fatal("migration lost the avatar reference")
	}
}

func TestPostgresSantaApplicationQuotaBoundsRoster(t *testing.T) {
	event := newSantaEvent(t, setup(t), "UTC", 5, false)
	guest := event.register(t, "Guest")
	_, err := event.db.Exec(event.ctx, `WITH people AS (
	    INSERT INTO users (email, display_name, password_hash)
	    SELECT 'quota-' || n || '@example.org', 'Quota', 'not-used' FROM generate_series(1, 200) n RETURNING id
	) INSERT INTO santa_applications (event_id, user_id) SELECT $1, id FROM people`, event.event.ID)
	must(t, err)
	status(t, event.call("POST", event.invite()+"/applications", guest, ""), 409)
	var count int
	must(t, event.db.QueryRow(event.ctx, "SELECT count(*) FROM santa_applications WHERE event_id = $1", event.event.ID).Scan(&count))
	if count != 200 {
		t.Fatal("event exceeded its roster quota")
	}
}

func TestPostgresPhotoQuotaBoundsBytesObjectsAndRate(t *testing.T) {
	for _, test := range []struct {
		name          string
		count         int
		size          int64
		owned, recent bool
		status        int
	}{
		{"hourly", 60, 1, true, true, 429},
		{"user objects", 1000, 1, true, false, 409},
		{"global bytes", 1024, 5 << 20, false, false, 503},
		{"global objects", 20000, 1, false, false, 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := setup(t)
			owner := f.register(t, "Owner")
			profile := decode[accounts.Profile](t, status(t, f.call("GET", "/me", owner, ""), 200))
			_, err := f.db.Exec(f.ctx, `INSERT INTO gift_photo_objects (id, owner_id, size_bytes, created_at)
			    SELECT 'seed-' || n, CASE WHEN $2 THEN $1::bigint END, $3,
			    clock_timestamp() - CASE WHEN $4 THEN interval '1 minute' ELSE interval '2 hours' END
			    FROM generate_series(1, $5::int) n`, profile.ID, test.owned, test.size, test.recent, test.count)
			must(t, err)
			err = photos.TrackUploads(f.ctx, f.db, profile.ID, []photos.UploadObject{{ID: platform.Token(), Size: 1}})
			var p *problem.Error
			if !errors.As(err, &p) || p.Status != test.status {
				t.Fatalf("expected %d, got %v", test.status, err)
			}
		})
	}
}

func TestPostgresRejectedUploadsLeaveNoObjectsAndShareRateLimit(t *testing.T) {
	f := setup(t)
	// Quota reservation must not wait for a second connection in this pool
	// while the referencing transaction holds its only connection.
	config := f.db.Config()
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(f.ctx, config)
	must(t, err)
	t.Cleanup(pool.Close)
	f.db = pool
	var puts atomic.Int64
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		puts.Add(1)
		w.Header().Set("ETag", `"test"`)
	}))
	defer s3.Close()
	t.Setenv("S3_ENDPOINT", s3.URL)
	t.Setenv("S3_BUCKET", "test")
	t.Setenv("S3_ACCESS_KEY_ID", "test")
	t.Setenv("S3_SECRET_ACCESS_KEY", "test")
	store, err := photos.FromEnv()
	must(t, err)
	f.handler = httpapi.HandlerWithPhotos(f.db, false, netip.Addr{}, store)
	owner, outsider := f.register(t, "Owner"), f.register(t, "Outsider")
	list, _ := f.newList(t, owner)
	for range 10 {
		status(t, uploadTestAvatar(t, f, outsider, ownerPath(list.ID)+"/avatar", testPNG(t, 2)), 404)
	}
	var count int
	must(t, f.db.QueryRow(f.ctx, "SELECT count(*) FROM gift_photo_objects").Scan(&count))
	if count != 0 || puts.Load() != 0 {
		t.Fatal("rejected uploads created storage objects")
	}
	// The same limit applies to profile and wishlist uploads.
	status(t, uploadTestAvatar(t, f, outsider, "/me/avatar", testPNG(t, 2)), 429)
	status(t, uploadTestAvatar(t, f, owner, "/me/avatar", testPNG(t, 2)), 200)
	if puts.Load() != 1 {
		t.Fatal("another user's upload was blocked")
	}
	var size int64
	must(t, f.db.QueryRow(f.ctx, "SELECT size_bytes FROM gift_photo_objects WHERE owner_id IS NOT NULL").Scan(&size))
	if size != int64(len(testPNG(t, 2))) {
		t.Fatal("uploaded bytes were not accounted")
	}
}

func TestPostgresPhotoCleanupContinuesPastFailuresAndBatches(t *testing.T) {
	f := setup(t)
	f.register(t, "Owner")
	_, err := f.db.Exec(f.ctx, `INSERT INTO gift_photo_objects (id, created_at)
	    SELECT 'cleanup-' || lpad(n::text, 3, '0'), clock_timestamp() - interval '2 hours' FROM generate_series(0, 205) n;
	    UPDATE users SET avatar_id = 'cleanup-205'`)
	must(t, err)
	failure := errors.New("storage unavailable for one object")
	count, err := (wishlists.Store{DB: f.db}).RemoveUnusedPhotos(f.ctx, func(_ context.Context, id string) error {
		if id == "cleanup-000" {
			return failure
		}
		if id == "cleanup-205" {
			t.Fatal("cleanup tried to delete a referenced avatar")
		}
		return nil
	})
	if !errors.Is(err, failure) || count != 204 {
		t.Fatalf("cleanup removed %d, error %v", count, err)
	}
	var remaining int
	must(t, f.db.QueryRow(f.ctx, "SELECT count(*) FROM gift_photo_objects").Scan(&remaining))
	if remaining != 2 {
		t.Fatalf("cleanup left %d objects", remaining)
	}
}

func TestPostgresSantaQuotasAndLegacyPagination(t *testing.T) {
	event := newSantaEvent(t, setup(t), "UTC", 5, true)
	f := event.fixture
	_, err := f.db.Exec(f.ctx, `INSERT INTO santa_events (organizer_id, title, exchange_date, time_zone, close_at, budget, currency, rules, invite_hash, created_at)
	    SELECT organizer_id, title, exchange_date, time_zone, close_at, budget, currency, rules,
	    decode(lpad(n::text, 64, '0'), 'hex'), clock_timestamp() - interval '2 hours'
	    FROM santa_events CROSS JOIN generate_series(1, 48) n WHERE id = $1`, event.event.ID)
	must(t, err)
	body := fmt.Sprintf(`{"title":"Last event","exchange_date":%q,"time_zone":"UTC","budget":"1","currency":"RUB","rules":""}`, event.view(event.organizer).ExchangeDate)
	responses := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { responses <- f.call("POST", "/santa/events", event.organizer, body) }()
	}
	a, b := <-responses, <-responses
	if a.Code == 409 {
		a, b = b, a
	}
	status(t, a, 201)
	status(t, b, 409)
	_, err = f.db.Exec(f.ctx, `INSERT INTO santa_events (organizer_id, title, exchange_date, time_zone, close_at, budget, currency, rules, invite_hash)
	    SELECT organizer_id, title, exchange_date, time_zone, close_at, budget, currency, rules,
	    decode(lpad(n::text, 64, '0'), 'hex') FROM santa_events CROSS JOIN generate_series(51, 55) n WHERE id = $1`, event.event.ID)
	must(t, err)
	first, second := testPages[[]santa.Event](t, f, event.organizer, "/santa/events?archived=false")
	if len(first) != 50 || len(second) != 5 || first[49].ID >= second[0].ID {
		t.Fatal("legacy events did not paginate")
	}
	_, err = f.db.Exec(f.ctx, "UPDATE santa_events SET close_at = clock_timestamp() - interval '1 second' WHERE id = $1", event.event.ID)
	must(t, err)
	archived := decode[[]santa.Event](t, status(t, f.call("GET", "/santa/events?archived=true", event.organizer, ""), 200))
	if len(archived) != 1 || archived[0].ID != event.event.ID {
		t.Fatal("archive filter lost the closed event")
	}
	status(t, f.call("GET", "/santa/events?after=invalid", event.organizer, ""), 400)
}

func TestPostgresSantaCreationRateIsDurable(t *testing.T) {
	event := newSantaEvent(t, setup(t), "UTC", 5, false)
	f := event.fixture
	body := fmt.Sprintf(`{"title":"Event","exchange_date":%q,"time_zone":"UTC","budget":"1","currency":"RUB","rules":""}`, event.view(event.organizer).ExchangeDate)
	for range 4 {
		status(t, f.call("POST", "/santa/events", event.organizer, body), 201)
	}
	f.handler = httpapi.Handler(f.db, false)
	status(t, f.call("POST", "/santa/events", event.organizer, body), 429)
	_, err := f.db.Exec(f.ctx, "UPDATE santa_events SET created_at = clock_timestamp() - interval '2 hours'")
	must(t, err)
	status(t, f.call("POST", "/santa/events", event.organizer, body), 201)
}
