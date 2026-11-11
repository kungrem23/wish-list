package httpapi

import (
	"mime"
	"net/http"
	"strconv"

	"wishlist/internal/history"
	"wishlist/internal/problem"
	"wishlist/internal/wishlists"
)

func (a api) owned(w http.ResponseWriter, r *http.Request, userID int64) error {
	cursor, err := readListCursor(r)
	if err != nil {
		return err
	}
	lists, err := a.wishlists.Owned(r.Context(), userID, cursor.Archived, cursor.Date, cursor.ID, pageSize+1)
	if err != nil {
		return err
	}
	lists = page(w, lists, func(list wishlists.Summary) listCursor {
		return listCursor{Archived: list.ArchivedAt != nil, Date: list.HolidayDate, ID: list.ID}
	})
	for i := range lists {
		list := &lists[i]
		list.AvatarURL = wishlistAvatarURL(wishlistPath(*list, true), list.AvatarID)
	}
	return writeJSON(w, http.StatusOK, lists)
}

func (a api) create(w http.ResponseWriter, r *http.Request, userID int64) error {
	input, err := readJSON[wishlists.Details](w, r)
	if err != nil {
		return err
	}
	list, err := a.wishlists.Create(r.Context(), userID, input)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, list)
}

func (a api) ownView(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	cursor, err := readGiftCursor(r)
	if err != nil {
		return err
	}
	view, err := a.wishlists.OwnView(r.Context(), userID, id, cursor, pageSize+1)
	if err != nil {
		return err
	}
	return a.writeListPage(w, view, cursor)
}

func (a api) sharedView(w http.ResponseWriter, r *http.Request, userID int64) error {
	cursor, err := readGiftCursor(r)
	if err != nil {
		return err
	}
	view, err := a.wishlists.SharedView(r.Context(), userID, r.PathValue("token"), cursor, pageSize+1)
	if err != nil {
		return err
	}
	return a.writeListPage(w, view, cursor)
}

func (a api) writeListPage(w http.ResponseWriter, view wishlists.View, cursor wishlists.GiftCursor) error {
	path := wishlistPath(view.Summary, view.IsOwner)
	view.AvatarURL = wishlistAvatarURL(path, view.AvatarID)
	return writeJSON(w, http.StatusOK, struct {
		wishlists.View
		Gifts               []giftWithPhotos `json:"gifts"`
		PhotoUploadsEnabled bool             `json:"photo_uploads_enabled,omitempty"`
	}{view, giftPage(w, path, view.Gifts, cursor), view.IsOwner && a.photos != nil})
}

func wishlistPath(list wishlists.Summary, owner bool) string {
	if owner {
		return "/api/v1/wishlists/" + strconv.FormatInt(list.ID, 10)
	}
	return "/api/v1/shared/" + list.ShareToken
}

type giftWithPhotos struct {
	wishlists.Gift
	Photos []photoReference `json:"photos,omitempty"`
}

func giftPage(w http.ResponseWriter, path string, gifts []wishlists.Gift, cursor wishlists.GiftCursor) []giftWithPhotos {
	gifts = page(w, gifts, cursor.Next)
	result := make([]giftWithPhotos, 0, len(gifts))
	for _, gift := range gifts {
		result = append(result, giftWithPhotos{Gift: gift, Photos: photoReferences(path, gift)})
	}
	return result
}

func (a api) update(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	input, err := readJSON[wishlists.Details](w, r)
	if err != nil {
		return err
	}
	return noContent(w, a.wishlists.Update(r.Context(), userID, id, input))
}

func (a api) archive(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	return noContent(w, a.wishlists.Archive(r.Context(), userID, id))
}

func (a api) copyUngifted(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	input, err := readJSON[wishlists.Details](w, r)
	if err != nil {
		return err
	}
	list, err := a.wishlists.CopyUngifted(r.Context(), userID, id, input)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, list)
}

func (a api) saveGift(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var gift int64
	if r.Method == http.MethodPut {
		gift, err = pathID(r, "gift")
		if err != nil {
			return err
		}
	}
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentType == "multipart/form-data" {
		return noContent(w, a.saveGiftWithPhotos(w, r, userID, id, gift))
	}
	card, err := readJSON[wishlists.Card](w, r)
	if err != nil {
		return err
	}
	return noContent(w, a.wishlists.SaveGift(r.Context(), userID, id, gift, card))
}

func (a api) hideGift(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	gift, err := pathID(r, "gift")
	if err != nil {
		return err
	}
	return noContent(w, a.wishlists.HideGift(r.Context(), userID, id, gift))
}

func (a api) setGifted(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	gift, err := pathID(r, "gift")
	if err != nil {
		return err
	}
	input, err := readJSON[struct {
		Gifted *bool `json:"gifted"`
	}](w, r)
	if err != nil {
		return err
	}
	if input.Gifted == nil {
		return problem.New(http.StatusBadRequest, "Укажите итог подарка")
	}
	return noContent(w, a.wishlists.SetGifted(r.Context(), userID, id, gift, *input.Gifted))
}

func (a api) reserve(w http.ResponseWriter, r *http.Request, userID int64) error {
	gift, err := pathID(r, "gift")
	if err != nil {
		return err
	}
	return noContent(w, a.wishlists.Reserve(r.Context(), userID, r.PathValue("token"), gift))
}

func (a api) cancel(w http.ResponseWriter, r *http.Request, userID int64) error {
	gift, err := pathID(r, "gift")
	if err != nil {
		return err
	}
	return noContent(w, a.wishlists.Cancel(r.Context(), userID, r.PathValue("token"), gift))
}

func (a api) cancelBooking(w http.ResponseWriter, r *http.Request, userID int64) error {
	gift, err := pathID(r, "gift")
	if err != nil {
		return err
	}
	return noContent(w, a.wishlists.CancelBooking(r.Context(), userID, gift))
}

func (a api) bookings(w http.ResponseWriter, r *http.Request, userID int64) error {
	cursor, err := readCursor[activityCursor](r)
	if err != nil || (cursor.At != nil && cursor.ID <= 0) {
		return errCursor
	}
	bookings, err := a.wishlists.Bookings(r.Context(), userID, cursor.At, cursor.ID, pageSize+1)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, page(w, bookings, func(booking wishlists.Booking) activityCursor {
		return activityCursor{At: &booking.ReservedAt, ID: booking.GiftID}
	}))
}

func (a api) listHistory(w http.ResponseWriter, r *http.Request, userID int64) error {
	cursor, err := readCursor[activityCursor](r)
	if err != nil || (cursor.At != nil && cursor.ID <= 0) {
		return errCursor
	}
	entries, err := a.history.List(r.Context(), userID, cursor.At, cursor.ID, pageSize+1)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, page(w, entries, func(entry history.Entry) activityCursor {
		return activityCursor{At: &entry.ReservedAt, ID: entry.GiftID}
	}))
}

func (a api) deleteHistory(w http.ResponseWriter, r *http.Request, userID int64) error {
	gift, err := pathID(r, "gift")
	if err != nil {
		return err
	}
	return noContent(w, a.history.Delete(r.Context(), userID, gift))
}
