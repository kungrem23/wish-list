package httpapi

import (
	"context"
	"net/http"

	"wishlist/internal/accounts"
	"wishlist/internal/wishlists"
)

func (a api) socialRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/users/{id}", a.private(a.userProfile))
	mux.Handle("PUT /api/v1/users/{id}/follow", a.private(a.follow))
	mux.Handle("DELETE /api/v1/users/{id}/follow", a.private(a.follow))
	mux.Handle("GET /api/v1/me/following", a.private(a.connections(false)))
	mux.Handle("GET /api/v1/me/followers", a.private(a.connections(true)))
	mux.Handle("GET /api/v1/me/addresses", a.private(a.addresses))
	mux.Handle("POST /api/v1/me/addresses", a.private(a.saveAddress))
	mux.Handle("PUT /api/v1/me/addresses/{id}", a.private(a.saveAddress))
	mux.Handle("DELETE /api/v1/me/addresses/{id}", a.private(a.deleteAddress))
	mux.Handle("PUT /api/v1/me/avatar", a.private(a.saveAvatar))
	mux.Handle("DELETE /api/v1/me/avatar", a.private(a.deleteAvatar))
	mux.Handle("GET /api/v1/users/{id}/avatar/{photo}", a.private(a.avatar))
}

func (a api) userProfile(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	return a.friendProfile(w, r, userID, id, "")
}

func (a api) sharedProfile(w http.ResponseWriter, r *http.Request, userID int64) error {
	return a.friendProfile(w, r, userID, 0, r.PathValue("token"))
}

func (a api) friendProfile(w http.ResponseWriter, r *http.Request, userID, id int64, token string) error {
	cursor, err := readListCursor(r)
	if err != nil || cursor.Archived {
		return errCursor
	}
	profile, err := a.wishlists.FriendProfile(r.Context(), userID, id, token, cursor.Date, cursor.ID, pageSize+1)
	if err != nil {
		return err
	}
	profile.Lists = page(w, profile.Lists, func(list wishlists.Summary) listCursor {
		return listCursor{Date: list.HolidayDate, ID: list.ID}
	})
	for i := range profile.Lists {
		list := &profile.Lists[i]
		list.AvatarURL = wishlistAvatarURL(wishlistPath(*list, profile.IsSelf), list.AvatarID)
	}
	return writeJSON(w, http.StatusOK, profile)
}

func (a api) follow(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if r.Method == http.MethodDelete {
		return noContent(w, a.accounts.Unfollow(r.Context(), userID, id))
	}
	return noContent(w, a.accounts.Follow(r.Context(), userID, id))
}

func (a api) connections(followers bool) privateEndpoint {
	return func(w http.ResponseWriter, r *http.Request, userID int64) error {
		after, err := readCursor[int64](r)
		if err != nil || after < 0 {
			return errCursor
		}
		people, err := a.accounts.Connections(r.Context(), userID, followers, after, pageSize+1)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, page(w, people, func(p accounts.Person) int64 { return p.ID }))
	}
}

func (a api) addresses(w http.ResponseWriter, r *http.Request, userID int64) error {
	addresses, err := a.accounts.Addresses(r.Context(), userID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, addresses)
}

func (a api) saveAddress(w http.ResponseWriter, r *http.Request, userID int64) error {
	var id int64
	var err error
	code := http.StatusCreated
	if r.Method == http.MethodPut {
		id, err = pathID(r, "id")
		if err != nil {
			return err
		}
		code = http.StatusOK
	}
	input, err := readJSON[accounts.Address](w, r)
	if err != nil {
		return err
	}
	address, err := a.accounts.SaveAddress(r.Context(), userID, id, input)
	if err != nil {
		return err
	}
	return writeJSON(w, code, address)
}

func (a api) deleteAddress(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	return noContent(w, a.accounts.DeleteAddress(r.Context(), userID, id))
}

func (a api) saveAvatar(w http.ResponseWriter, r *http.Request, userID int64) error {
	err := a.uploadAvatar(w, r, func(id string, upload func(context.Context) error) error {
		return a.accounts.SetAvatar(r.Context(), userID, id, upload)
	})
	if err != nil {
		return err
	}
	return a.profile(w, r, userID)
}

func (a api) deleteAvatar(w http.ResponseWriter, r *http.Request, userID int64) error {
	if err := a.accounts.DeleteAvatar(r.Context(), userID); err != nil {
		return err
	}
	return a.profile(w, r, userID)
}

func (a api) avatar(w http.ResponseWriter, r *http.Request, _ int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	photo := r.PathValue("photo")
	if !validPhotoID(photo) {
		return errPhotoNotFound
	}
	exists, err := a.accounts.HasAvatar(r.Context(), id, photo)
	if err != nil {
		return err
	}
	if !exists {
		return errPhotoNotFound
	}
	return a.streamPhoto(w, r, photo)
}
