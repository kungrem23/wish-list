package wishlists

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"wishlist/internal/photos"
	"wishlist/internal/problem"
)

type Card struct {
	Title       string  `json:"title"`
	Description *string `json:"description"`
	ProductURL  *string `json:"product_url"`
	Price       *string `json:"price"`
	Priority    string  `json:"priority"`
}

type Gift struct {
	ID int64 `json:"id,string"`
	Card
	Active      bool      `json:"active"`
	Reservation string    `json:"reservation,omitempty"`
	Gifted      *bool     `json:"gifted,omitempty"`
	PhotoIDs    []string  `json:"-"`
	CreatedAt   time.Time `json:"-"`
}

const maxGiftsPerList = 100
const MaxGiftPhotos = 5

type PhotoChanges struct {
	Add    []string
	Remove []string
	Upload photos.Upload
}

var pricePattern = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,2})?$`)

func (c Card) validate() (Card, error) {
	c.Title = strings.TrimSpace(c.Title)
	c.Description = trimOptional(c.Description)
	c.ProductURL = trimOptional(c.ProductURL)
	if c.Title == "" || utf8.RuneCountInString(c.Title) > 150 {
		return c, problem.New(http.StatusBadRequest, "Название подарка должно содержать от 1 до 150 символов")
	}
	if c.Description != nil && utf8.RuneCountInString(*c.Description) > 4000 {
		return c, problem.New(http.StatusBadRequest, "Описание должно содержать не более 4000 символов")
	}
	if c.ProductURL != nil {
		link, err := url.Parse(*c.ProductURL)
		if err != nil || len(*c.ProductURL) > 4096 || link.Hostname() == "" || (link.Scheme != "http" && link.Scheme != "https") {
			return c, problem.New(http.StatusBadRequest, "Укажите полную ссылку на товар с http:// или https://")
		}
	}
	if c.Price != nil {
		whole, _, _ := strings.Cut(*c.Price, ".")
		if !pricePattern.MatchString(*c.Price) || len(strings.TrimLeft(whole, "0")) > 10 {
			return c, problem.New(http.StatusBadRequest, "Укажите цену от 0 до 9999999999.99 с точностью до копеек")
		}
	}
	switch c.Priority {
	case "":
		c.Priority = "normal"
	case "high", "normal", "low":
	default:
		return c, problem.New(http.StatusBadRequest, "Укажите допустимый приоритет подарка")
	}
	return c, nil
}

func trimOptional(value *string) *string {
	if value != nil {
		trimmed := strings.TrimSpace(*value)
		if trimmed != "" {
			return &trimmed
		}
	}
	return nil
}

func (s Store) SaveGift(ctx context.Context, ownerID, listID, giftID int64, card Card) error {
	return s.SaveGiftWithPhotos(ctx, ownerID, listID, giftID, card, PhotoChanges{})
}

func (s Store) SaveGiftWithPhotos(ctx context.Context, ownerID, listID, giftID int64, card Card, changes PhotoChanges) error {
	return s.withOwnedListLocked(ctx, ownerID, listID, func(tx pgx.Tx, l list, now time.Time) error {
		if err := l.requireEditing(now); err != nil {
			return err
		}
		card, err := card.validate()
		if err != nil {
			return err
		}
		photoIDs := make([]string, 0, MaxGiftPhotos)
		if giftID != 0 {
			err := tx.QueryRow(ctx, `
				SELECT photo_ids FROM gifts WHERE wishlist_id = $1 AND id = $2 AND active
			`, l.ID, giftID).Scan(&photoIDs)
			if errors.Is(err, pgx.ErrNoRows) {
				return errNotFound
			}
			if err != nil {
				return err
			}
		}
		photoIDs = slices.DeleteFunc(photoIDs, func(id string) bool { return slices.Contains(changes.Remove, id) })
		photoIDs = append(photoIDs, changes.Add...)
		if len(photoIDs) > MaxGiftPhotos {
			return problem.New(http.StatusBadRequest, "На карточке может быть не больше 5 фотографий")
		}
		if giftID == 0 {
			var full bool
			err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM gifts WHERE wishlist_id = $1 OFFSET $2)`,
				l.ID, maxGiftsPerList-1).Scan(&full)
			if err != nil {
				return err
			}
			if full {
				return errGiftQuota
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO gifts (wishlist_id, title, description, product_url, photo_ids, price, priority)
				VALUES ($1, $2, $3, $4, $5, $6::text::numeric, $7::text::gift_priority)
			`, l.ID, card.Title, card.Description, card.ProductURL, photoIDs, card.Price, card.Priority)
			if err != nil {
				return err
			}
		} else {
			_, err := tx.Exec(ctx, `
				UPDATE gifts SET title = $3, description = $4, product_url = $5, photo_ids = $6,
				    price = $7::text::numeric, priority = $8::text::gift_priority
				WHERE wishlist_id = $1 AND id = $2 AND active
			`, l.ID, giftID, card.Title, card.Description, card.ProductURL, photoIDs, card.Price, card.Priority)
			if err != nil {
				return err
			}
		}
		if changes.Upload != nil {
			return changes.Upload(ctx, tx)
		}
		return nil
	})
}

func (s Store) HideGift(ctx context.Context, ownerID, listID, giftID int64) error {
	return s.withOwnedListLocked(ctx, ownerID, listID, func(tx pgx.Tx, l list, now time.Time) error {
		if err := l.requireEditing(now); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE gifts SET active = false WHERE wishlist_id = $1 AND id = $2`, l.ID, giftID)
		if err == nil && tag.RowsAffected() == 0 {
			return errNotFound
		}
		return err
	})
}

func (s Store) SetGifted(ctx context.Context, ownerID, listID, giftID int64, gifted bool) error {
	return s.withOwnedListLocked(ctx, ownerID, listID, func(tx pgx.Tx, l list, _ time.Time) error {
		if l.ArchivedAt == nil {
			return errArchive
		}
		tag, err := tx.Exec(ctx, `
			UPDATE gifts SET gifted = $3 WHERE wishlist_id = $1 AND id = $2 AND visible_in_archive
		`, l.ID, giftID, gifted)
		if err == nil && tag.RowsAffected() == 0 {
			return errNotFound
		}
		return err
	})
}

var (
	errGiftQuota   = problem.New(http.StatusConflict, "Достигнут лимит в 100 карточек на список, включая скрытые")
	errUnavailable = problem.New(http.StatusConflict, "Подарок недоступен для бронирования")
	errReserved    = problem.New(http.StatusConflict, "Подарок уже забронирован")
	errOwnList     = problem.New(http.StatusForbidden, "Владелец не может бронировать или отменять брони в своём списке")
)

func (l list) requireBooking(userID int64, now time.Time) error {
	if l.ownerID == userID {
		return errOwnList
	}
	if !l.open(now) {
		return errClosed
	}
	return nil
}

func (s Store) Reserve(ctx context.Context, userID int64, token string, giftID int64) error {
	return s.writeTx(ctx, func(tx pgx.Tx) error {
		l, err := lockList(ctx, tx, "share_token = $1", token)
		if err != nil {
			return err
		}
		return reserveGift(ctx, tx, l, userID, giftID, time.Time{})
	})
}

// ReserveFromEvent uses the caller's transaction, which holds the current assignment in place.
func ReserveFromEvent(ctx context.Context, tx pgx.Tx, userID, listID, giftID int64, eventClose time.Time) error {
	l, err := lockList(ctx, tx, "id = $1", listID)
	if err != nil {
		return err
	}
	return reserveGift(ctx, tx, l, userID, giftID, eventClose)
}

func reserveGift(ctx context.Context, tx pgx.Tx, l list, userID, giftID int64, eventClose time.Time) error {
	// A redraw locks the current givers before checking for reservations,
	// so a booking made through either route is ordered against that check.
	var id int64
	if err := tx.QueryRow(ctx, "SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE", userID).Scan(&id); err != nil {
		return err
	}
	now, err := dbTime(ctx, tx)
	if err != nil {
		return err
	}
	viaSanta := !eventClose.IsZero()
	if viaSanta && (!now.Before(eventClose) || !l.open(now)) {
		return errNotFound
	}
	if err := l.requireBooking(userID, now); err != nil {
		return err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT active FROM gifts WHERE wishlist_id = $1 AND id = $2`, l.ID, giftID).Scan(&active)
	if err == pgx.ErrNoRows || (err == nil && !active) {
		return errUnavailable
	}
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO reservations (gift_id, user_id, via_santa) VALUES ($1, $2, $3)
		ON CONFLICT (gift_id) DO NOTHING
	`, giftID, userID, viaSanta)
	if err == nil && tag.RowsAffected() == 0 {
		return errReserved
	}
	return err
}

func (s Store) Cancel(ctx context.Context, userID int64, token string, giftID int64) error {
	return s.withLocked(ctx, "share_token = $1", []any{token}, func(tx pgx.Tx, l list, now time.Time) error {
		return cancelGift(ctx, tx, l, now, userID, giftID)
	})
}

func CancelFromEvent(ctx context.Context, tx pgx.Tx, userID, listID, giftID int64, eventClose time.Time) error {
	l, err := lockList(ctx, tx, "id = $1", listID)
	if err != nil {
		return err
	}
	now, err := dbTime(ctx, tx)
	if err != nil {
		return err
	}
	if !now.Before(eventClose) || !l.open(now) {
		return errNotFound
	}
	return cancelGift(ctx, tx, l, now, userID, giftID)
}

func (s Store) CancelBooking(ctx context.Context, userID, giftID int64) error {
	return s.withLocked(ctx, `id = (
		SELECT g.wishlist_id FROM gifts g JOIN reservations r ON r.gift_id = g.id
		WHERE g.id = $1 AND r.user_id = $2
	)`, []any{giftID, userID}, func(tx pgx.Tx, l list, now time.Time) error {
		return cancelGift(ctx, tx, l, now, userID, giftID)
	})
}

func cancelGift(ctx context.Context, tx pgx.Tx, l list, now time.Time, userID, giftID int64) error {
	if err := l.requireBooking(userID, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		DELETE FROM reservations r USING gifts g
		WHERE r.gift_id = g.id AND r.gift_id = $2 AND r.user_id = $3 AND g.wishlist_id = $1
	`, l.ID, giftID, userID)
	return err
}
