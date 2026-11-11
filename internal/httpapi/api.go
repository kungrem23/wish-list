package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wishlist/internal/accounts"
	"wishlist/internal/history"
	"wishlist/internal/photos"
	"wishlist/internal/problem"
	"wishlist/internal/santa"
	"wishlist/internal/wishlists"
)

const cookieName = "wishlist_session"

type api struct {
	accounts      accounts.Store
	wishlists     wishlists.Store
	history       history.Store
	santa         santa.Store
	photos        *photos.Store
	authLimits    *authLimits
	uploadLimits  *authAttemptLimit
	uploadSlots   chan struct{}
	secureCookies bool
}

type endpoint func(http.ResponseWriter, *http.Request) error
type privateEndpoint func(http.ResponseWriter, *http.Request, int64) error

func Handler(db *pgxpool.Pool, secureCookies bool) http.Handler {
	return HandlerWithTrustedProxy(db, secureCookies, netip.Addr{})
}

func HandlerWithTrustedProxy(db *pgxpool.Pool, secureCookies bool, trustedProxy netip.Addr) http.Handler {
	return HandlerWithPhotos(db, secureCookies, trustedProxy, nil)
}

func HandlerWithPhotos(db *pgxpool.Pool, secureCookies bool, trustedProxy netip.Addr, photoStore *photos.Store) http.Handler {
	a := api{
		accounts: accounts.Store{DB: db}, wishlists: wishlists.Store{DB: db},
		history: history.Store{DB: db}, santa: santa.Store{DB: db}, photos: photoStore, authLimits: newAuthLimits(trustedProxy), secureCookies: secureCookies,
		uploadLimits: &authAttemptLimit{attempts: 10, keys: make(map[string]authAttempts)},
		uploadSlots:  make(chan struct{}, 4),
	}
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/register", respond(a.authLimits.byIP(a.register)))
	mux.Handle("POST /api/v1/login", respond(a.authLimits.byIP(a.login)))
	mux.Handle("POST /api/v1/logout", respond(a.logout))
	a.socialRoutes(mux)
	mux.Handle("GET /api/v1/me", a.private(a.profile))
	mux.Handle("PUT /api/v1/me", a.private(a.updateProfile))

	mux.Handle("GET /api/v1/wishlists", a.private(a.owned))
	mux.Handle("POST /api/v1/wishlists", a.private(a.create))
	mux.Handle("GET /api/v1/wishlists/{id}", a.private(a.ownView))
	mux.Handle("PUT /api/v1/wishlists/{id}", a.private(a.update))
	mux.Handle("PUT /api/v1/wishlists/{id}/avatar", a.private(a.saveWishlistAvatar))
	mux.Handle("DELETE /api/v1/wishlists/{id}/avatar", a.private(a.saveWishlistAvatar))
	mux.Handle("GET /api/v1/wishlists/{id}/avatar/{photo}", a.private(a.wishlistAvatar))
	mux.Handle("POST /api/v1/wishlists/{id}/archive", a.private(a.archive))
	mux.Handle("POST /api/v1/wishlists/{id}/copy", a.private(a.copyUngifted))
	mux.Handle("POST /api/v1/wishlists/{id}/gifts", a.private(a.saveGift))
	mux.Handle("PUT /api/v1/wishlists/{id}/gifts/{gift}", a.private(a.saveGift))
	mux.Handle("DELETE /api/v1/wishlists/{id}/gifts/{gift}", a.private(a.hideGift))
	mux.Handle("PUT /api/v1/wishlists/{id}/gifts/{gift}/outcome", a.private(a.setGifted))
	mux.Handle("GET /api/v1/wishlists/{id}/gifts/{gift}/photos/{photo}", a.private(a.giftPhoto))

	mux.Handle("GET /api/v1/shared/{token}", a.private(a.sharedView))
	mux.Handle("GET /api/v1/shared/{token}/avatar/{photo}", a.private(a.wishlistAvatar))
	mux.Handle("GET /api/v1/shared/{token}/profile", a.private(a.sharedProfile))
	mux.Handle("POST /api/v1/shared/{token}/gifts/{gift}/reservation", a.private(a.reserve))
	mux.Handle("DELETE /api/v1/shared/{token}/gifts/{gift}/reservation", a.private(a.cancel))
	mux.Handle("GET /api/v1/shared/{token}/gifts/{gift}/photos/{photo}", a.private(a.giftPhoto))
	mux.Handle("GET /api/v1/reservations", a.private(a.bookings))
	mux.Handle("GET /api/v1/activity/gifts/{gift}/photos/{photo}", a.private(a.activityGiftPhoto))
	mux.Handle("DELETE /api/v1/reservations/{gift}", a.private(a.cancelBooking))
	mux.Handle("GET /api/v1/history", a.private(a.listHistory))
	mux.Handle("DELETE /api/v1/history/{gift}", a.private(a.deleteHistory))

	a.santaRoutes(mux)

	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(respond(func(http.ResponseWriter, *http.Request) error {
		return problem.New(http.StatusForbidden, "Запрос с другого сайта отклонён")
	}))
	protected := protection.Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		protected.ServeHTTP(w, r.WithContext(ctx))
	})
}

func respond(fn endpoint) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		if err == nil {
			return
		}
		var p *problem.Error
		if errors.As(err, &p) {
			_ = writeJSON(w, p.Status, map[string]string{"error": p.Message})
			return
		}
		slog.Error("API request failed", "route", r.Pattern, "error", err)
		_ = writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Ошибка сервера"})
	})
}

func (a api) private(fn privateEndpoint) http.Handler {
	return respond(func(w http.ResponseWriter, r *http.Request) error {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			return accounts.ErrSession
		}
		id, err := a.accounts.UserID(r.Context(), cookie.Value)
		if err != nil {
			return err
		}
		return fn(w, r, id)
	})
}

func readJSON[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentType != "application/json" {
		var zero T
		return zero, problem.New(http.StatusUnsupportedMediaType, "Ожидается application/json")
	}
	return decodeJSON[T](http.MaxBytesReader(w, r.Body, maxJSONBytes))
}

const maxJSONBytes = 64 << 10

func decodeJSON[T any](body io.Reader) (T, error) {
	var input *T
	var zero T
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input == nil {
		return zero, problem.New(http.StatusBadRequest, "Некорректный JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return zero, problem.New(http.StatusBadRequest, "Ожидается один объект JSON")
	}
	return *input, nil
}

func writeJSON(w http.ResponseWriter, status int, data any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("write JSON response", "error", err)
	}
	return nil
}

func noContent(w http.ResponseWriter, err error) error {
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
	}
	return err
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, problem.New(http.StatusNotFound, "Некорректный идентификатор")
	}
	return id, nil
}
