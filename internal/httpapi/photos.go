package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"wishlist/internal/photos"
	"wishlist/internal/platform"
	"wishlist/internal/problem"
	"wishlist/internal/wishlists"
)

const maxPhotoBytes = 5 << 20

var (
	errPhotoSize        = problem.New(http.StatusRequestEntityTooLarge, "Максимальный размер одной фотографии — 5 МБ")
	errPhotoFormat      = problem.New(http.StatusBadRequest, "Выберите фотографию в формате JPEG, PNG или WebP")
	errPhotoNotFound    = problem.New(http.StatusNotFound, "Фотография не найдена")
	errPhotoUnavailable = problem.New(http.StatusServiceUnavailable, "Хранилище фотографий недоступно")
)

type photoReference struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func photoReferences(giftPath string, ids []string) []photoReference {
	var result []photoReference
	for _, id := range ids {
		result = append(result, photoReference{id, giftPath + "/photos/" + id})
	}
	return result
}

func activityPhotoReferences(giftID int64, ids []string) []photoReference {
	return photoReferences("/api/v1/activity/gifts/"+strconv.FormatInt(giftID, 10), ids)
}

func validPhotoID(id string) bool {
	data, err := base64.RawURLEncoding.Strict().DecodeString(id)
	return err == nil && len(id) == 43 && len(data) == 32
}

func (a api) saveGiftWithPhotos(w http.ResponseWriter, r *http.Request, userID, listID, giftID int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, wishlists.MaxGiftPhotos*maxPhotoBytes+2*maxJSONBytes)
	if err := r.ParseMultipartForm(512 << 10); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || errors.Is(err, multipart.ErrMessageTooLarge) {
			return problem.New(http.StatusRequestEntityTooLarge, "Запрос с фотографиями слишком большой")
		}
		return problem.New(http.StatusBadRequest, "Некорректная форма подарка")
	}
	defer r.MultipartForm.RemoveAll()

	cardFields := r.MultipartForm.Value["card"]
	if len(cardFields) != 1 || len(cardFields[0]) > maxJSONBytes {
		return problem.New(http.StatusBadRequest, "Передайте одну карточку подарка")
	}
	card, err := decodeJSON[wishlists.Card](strings.NewReader(cardFields[0]))
	if err != nil {
		return err
	}
	files := r.MultipartForm.File["photo"]
	removed := r.MultipartForm.Value["remove_photo"]
	if len(files) > wishlists.MaxGiftPhotos || len(removed) > wishlists.MaxGiftPhotos {
		return problem.New(http.StatusBadRequest, "На карточке может быть не больше 5 фотографий")
	}
	changes := wishlists.PhotoChanges{Remove: removed}
	for _, id := range removed {
		if !validPhotoID(id) {
			return problem.New(http.StatusBadRequest, "Некорректный идентификатор фотографии")
		}
	}
	if len(files) > 0 {
		if a.photos == nil {
			return errPhotoUnavailable
		}
		contentTypes := make([]string, 0, len(files))
		for _, file := range files {
			contentType, err := photoContentType(file)
			if err != nil {
				return err
			}
			contentTypes = append(contentTypes, contentType)
			changes.Add = append(changes.Add, platform.Token())
		}
		if err := a.wishlists.TrackPhotoUploads(r.Context(), changes.Add); err != nil {
			return err
		}
		changes.Upload = func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			for i, file := range files {
				if err := a.uploadPhoto(ctx, changes.Add[i], contentTypes[i], file); err != nil {
					slog.Error("upload gift photo", "error", err)
					return errPhotoUnavailable
				}
			}
			return nil
		}
	}
	return a.wishlists.SaveGiftWithPhotos(r.Context(), userID, listID, giftID, card, changes)
}

func photoContentType(header *multipart.FileHeader) (string, error) {
	if header.Size > maxPhotoBytes {
		return "", errPhotoSize
	}
	file, err := header.Open()
	if err != nil {
		return "", err
	}
	defer file.Close()
	config, format, err := image.DecodeConfig(file)
	if err != nil || (format != "jpeg" && format != "png" && format != "webp") {
		return "", errPhotoFormat
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 8192 || config.Height > 8192 {
		return "", problem.New(http.StatusBadRequest, "Сторона фотографии не должна превышать 8192 пикселя")
	}
	return "image/" + format, nil
}

func (a api) uploadPhoto(ctx context.Context, id, contentType string, header *multipart.FileHeader) error {
	file, err := header.Open()
	if err != nil {
		return err
	}
	defer file.Close()
	return a.photos.Put(ctx, id, contentType, header.Size, file)
}

func (a api) giftPhoto(w http.ResponseWriter, r *http.Request, userID int64) error {
	giftID, err := pathID(r, "gift")
	if err != nil {
		return err
	}
	photoID := r.PathValue("photo")
	if !validPhotoID(photoID) {
		return errPhotoNotFound
	}
	if token := r.PathValue("token"); token != "" {
		err = a.wishlists.SharedGiftPhoto(r.Context(), userID, giftID, photoID, token)
	} else {
		listID, pathErr := pathID(r, "id")
		if pathErr != nil {
			return pathErr
		}
		err = a.wishlists.OwnGiftPhoto(r.Context(), userID, listID, giftID, photoID)
	}
	if err != nil {
		return err
	}
	return a.streamPhoto(w, r, photoID)
}

func (a api) activityGiftPhoto(w http.ResponseWriter, r *http.Request, userID int64) error {
	giftID, err := pathID(r, "gift")
	if err != nil {
		return err
	}
	photoID := r.PathValue("photo")
	if !validPhotoID(photoID) {
		return errPhotoNotFound
	}
	if err := a.wishlists.ActivityGiftPhoto(r.Context(), userID, giftID, photoID); err != nil {
		return err
	}
	return a.streamPhoto(w, r, photoID)
}

func (a api) streamPhoto(w http.ResponseWriter, r *http.Request, photoID string) error {
	if a.photos == nil {
		return errPhotoUnavailable
	}
	object, err := a.photos.Get(r.Context(), photoID)
	if errors.Is(err, photos.ErrNotFound) {
		return errPhotoNotFound
	}
	if err != nil {
		slog.Error("read gift photo", "error", err)
		return errPhotoUnavailable
	}
	defer object.Body.Close()
	switch object.ContentType {
	case "image/jpeg", "image/png", "image/webp":
		w.Header().Set("Content-Type", object.ContentType)
	default:
		return errPhotoNotFound
	}
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if object.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
	}
	if r.Method != http.MethodHead {
		if _, err := io.Copy(w, object.Body); err != nil {
			slog.Error("stream gift photo", "error", err)
		}
	}
	return nil
}

func (a api) uploadAvatar(w http.ResponseWriter, r *http.Request, attach func(string, func(context.Context) error) error) error {
	if a.photos == nil {
		return errPhotoUnavailable
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes+maxJSONBytes)
	if err := r.ParseMultipartForm(512 << 10); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || errors.Is(err, multipart.ErrMessageTooLarge) {
			return errPhotoSize
		}
		return problem.New(http.StatusBadRequest, "Некорректная форма аватарки")
	}
	defer r.MultipartForm.RemoveAll()
	files := r.MultipartForm.File["photo"]
	if len(files) != 1 {
		return problem.New(http.StatusBadRequest, "Выберите одну аватарку")
	}
	file := files[0]
	contentType, err := photoContentType(file)
	if err != nil {
		return err
	}
	id := platform.Token()
	if err := a.wishlists.TrackPhotoUploads(r.Context(), []string{id}); err != nil {
		return err
	}
	return attach(id, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		if err := a.uploadPhoto(ctx, id, contentType, file); err != nil {
			slog.Error("upload avatar", "error", err)
			return errPhotoUnavailable
		}
		return nil
	})
}

func wishlistAvatarURL(listPath string, photoID *string) *string {
	if photoID == nil {
		return nil
	}
	url := listPath + "/avatar/" + *photoID
	return &url
}

func (a api) saveWishlistAvatar(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if r.Method == http.MethodDelete {
		return noContent(w, a.wishlists.SetAvatar(r.Context(), userID, id, nil, nil))
	}
	return noContent(w, a.uploadAvatar(w, r, func(photoID string, upload func(context.Context) error) error {
		return a.wishlists.SetAvatar(r.Context(), userID, id, &photoID, upload)
	}))
}

func (a api) wishlistAvatar(w http.ResponseWriter, r *http.Request, userID int64) error {
	photo := r.PathValue("photo")
	if !validPhotoID(photo) {
		return errPhotoNotFound
	}
	var err error
	if token := r.PathValue("token"); token != "" {
		err = a.wishlists.SharedAvatar(r.Context(), userID, token, photo)
	} else {
		id, pathErr := pathID(r, "id")
		if pathErr != nil {
			return pathErr
		}
		err = a.wishlists.OwnAvatar(r.Context(), userID, id, photo)
	}
	if err != nil {
		return err
	}
	return a.streamPhoto(w, r, photo)
}
