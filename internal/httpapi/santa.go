package httpapi

import (
	"errors"
	"fmt"
	"math"
	"net/http"

	"wishlist/internal/problem"
	"wishlist/internal/santa"
	"wishlist/internal/wishlists"
)

type eventEndpoint func(http.ResponseWriter, *http.Request, int64, int64) error
type grantEndpoint func(http.ResponseWriter, *http.Request, int64, santa.Grant) error

func (a api) santaRoutes(mux *http.ServeMux) {
	const events = "/api/v1/santa/events"
	const event = events + "/{id}"
	const grant = event + "/rounds/{round}/wishlists/{list}"
	const invite = "/api/v1/santa/invitations/{token}"
	mux.Handle("GET "+events, a.private(a.santaEvents))
	mux.Handle("POST "+events, a.private(a.santaCreate))
	mux.Handle("GET "+invite, a.private(a.santaInvitation))
	mux.Handle("POST "+invite+"/applications", a.private(a.santaApply))
	mux.Handle("GET "+event, a.eventEndpoint(a.santaEvent))
	mux.Handle("POST "+event+"/invitation", a.eventEndpoint(a.santaRotate))
	mux.Handle("PUT "+event+"/applications/{user}", a.eventEndpoint(a.santaDecide))
	mux.Handle("POST "+event+"/draw", a.eventEndpoint(a.santaDraw))
	mux.Handle("PUT "+event+"/wishlist", a.eventEndpoint(a.santaBind))
	mux.Handle("POST "+event+"/wishlist", a.eventEndpoint(a.santaCreateWishlist))
	mux.Handle("GET "+grant, a.grantEndpoint(a.santaWishlist))
	mux.Handle("GET "+grant+"/avatar/{photo}", a.grantEndpoint(a.santaAvatar))
	mux.Handle("GET "+grant+"/gifts/{gift}/photos/{photo}", a.grantEndpoint(a.santaPhoto))
	mux.Handle("POST "+grant+"/gifts/{gift}/reservation", a.grantEndpoint(a.santaReserve))
	mux.Handle("DELETE "+grant+"/gifts/{gift}/reservation", a.grantEndpoint(a.santaReserve))
}

func (a api) eventEndpoint(fn eventEndpoint) http.Handler {
	return a.private(func(w http.ResponseWriter, r *http.Request, userID int64) error {
		id, err := pathID(r, "id")
		if err != nil {
			return err
		}
		return fn(w, r, userID, id)
	})
}

func (a api) grantEndpoint(fn grantEndpoint) http.Handler {
	return a.eventEndpoint(func(w http.ResponseWriter, r *http.Request, userID, id int64) error {
		round, err := pathID(r, "round")
		if err != nil || round > math.MaxInt32 {
			return problem.New(http.StatusNotFound, "Раунд не найден")
		}
		list, err := pathID(r, "list")
		if err != nil {
			return err
		}
		return fn(w, r, userID, santa.Grant{EventID: id, Round: int(round), WishlistID: list})
	})
}

func (a api) santaEvents(w http.ResponseWriter, r *http.Request, userID int64) error {
	events, err := a.santa.List(r.Context(), userID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, events)
}

func (a api) santaCreate(w http.ResponseWriter, r *http.Request, userID int64) error {
	input, err := readJSON[santa.CreateInput](w, r)
	if err != nil {
		return err
	}
	event, err := a.santa.Create(r.Context(), userID, input)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, event)
}

func (a api) santaEvent(w http.ResponseWriter, r *http.Request, userID, id int64) error {
	view, err := a.santa.View(r.Context(), userID, id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, view)
}

func (a api) santaInvitation(w http.ResponseWriter, r *http.Request, userID int64) error {
	invite, err := a.santa.Invitation(r.Context(), userID, r.PathValue("token"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, invite)
}

func (a api) santaRotate(w http.ResponseWriter, r *http.Request, userID, id int64) error {
	invite, err := a.santa.Rotate(r.Context(), userID, id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, invite)
}

func (a api) santaApply(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := a.santa.Apply(r.Context(), userID, r.PathValue("token"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, struct {
		ID int64 `json:"id,string"`
	}{id})
}

func (a api) santaDecide(w http.ResponseWriter, r *http.Request, userID, id int64) error {
	applicant, err := pathID(r, "user")
	if err != nil {
		return err
	}
	input, err := readJSON[santa.Decision](w, r)
	if err != nil {
		return err
	}
	return noContent(w, a.santa.Decide(r.Context(), userID, id, applicant, input))
}

func (a api) santaDraw(w http.ResponseWriter, r *http.Request, userID, id int64) error {
	input, err := readJSON[santa.DrawInput](w, r)
	if err != nil {
		return err
	}
	err = a.santa.Draw(r.Context(), userID, id, input)
	var confirmation *santa.ConfirmationRequired
	if errors.As(err, &confirmation) {
		return writeJSON(w, http.StatusConflict, struct {
			Error string `json:"error"`
			Round int    `json:"confirm_round"`
		}{confirmation.Error(), confirmation.Round})
	}
	return noContent(w, err)
}

func (a api) santaBind(w http.ResponseWriter, r *http.Request, userID, id int64) error {
	input, err := readJSON[struct {
		WishlistID *int64 `json:"wishlist_id,string"`
	}](w, r)
	if err != nil {
		return err
	}
	return noContent(w, a.santa.Bind(r.Context(), userID, id, input.WishlistID))
}

func (a api) santaCreateWishlist(w http.ResponseWriter, r *http.Request, userID, id int64) error {
	input, err := readJSON[wishlists.Details](w, r)
	if err != nil {
		return err
	}
	list, err := a.santa.CreateWishlist(r.Context(), userID, id, input)
	if err != nil {
		return err
	}
	list.ShareToken = ""
	return writeJSON(w, http.StatusCreated, list)
}

func (a api) santaWishlist(w http.ResponseWriter, r *http.Request, userID int64, grant santa.Grant) error {
	cursor, err := readGiftCursor(r)
	if err != nil {
		return err
	}
	view, err := a.santa.Wishlist(r.Context(), userID, grant, cursor, pageSize+1)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/api/v1/santa/events/%d/rounds/%d/wishlists/%d", grant.EventID, grant.Round, grant.WishlistID)
	return writeJSON(w, http.StatusOK, struct {
		wishlists.GuestView
		Gifts     []giftWithPhotos `json:"gifts"`
		AvatarURL *string          `json:"avatar_url"`
	}{view, giftPage(w, path, view.Gifts, cursor), wishlistAvatarURL(path, view.AvatarID)})
}

func (a api) santaAvatar(w http.ResponseWriter, r *http.Request, userID int64, grant santa.Grant) error {
	photo := r.PathValue("photo")
	if !validPhotoID(photo) {
		return errPhotoNotFound
	}
	if err := a.santa.Avatar(r.Context(), userID, grant, photo); err != nil {
		return err
	}
	return a.streamPhoto(w, r, photo)
}

func (a api) santaPhoto(w http.ResponseWriter, r *http.Request, userID int64, grant santa.Grant) error {
	gift, err := pathID(r, "gift")
	if err != nil {
		return err
	}
	photo := r.PathValue("photo")
	if !validPhotoID(photo) {
		return errPhotoNotFound
	}
	if err := a.santa.Photo(r.Context(), userID, grant, gift, photo); err != nil {
		return err
	}
	return a.streamPhoto(w, r, photo)
}

func (a api) santaReserve(w http.ResponseWriter, r *http.Request, userID int64, grant santa.Grant) error {
	gift, err := pathID(r, "gift")
	if err != nil {
		return err
	}
	if r.Method == http.MethodDelete {
		return noContent(w, a.santa.Cancel(r.Context(), userID, grant, gift))
	}
	return noContent(w, a.santa.Reserve(r.Context(), userID, grant, gift))
}
