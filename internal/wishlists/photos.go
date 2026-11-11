package wishlists

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"wishlist/internal/photos"
)

func (s Store) OwnGiftPhoto(ctx context.Context, userID, listID, giftID int64, photoID string) error {
	return giftPhoto(ctx, s.DB, userID, giftID, photoID, "w.id = $4 AND w.owner_id = $1", listID)
}

func (s Store) SharedGiftPhoto(ctx context.Context, userID, giftID int64, photoID, token string) error {
	return giftPhoto(ctx, s.DB, userID, giftID, photoID, "w.share_token = $4", token)
}

// ActivityGiftPhoto preserves photo access through the reservation-to-history transition.
func (s Store) ActivityGiftPhoto(ctx context.Context, userID, giftID int64, photoID string) error {
	var visible bool
	err := s.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM gifts g WHERE g.id = $2 AND $3 = ANY(g.photo_ids)
			AND (EXISTS (SELECT 1 FROM reservations r WHERE r.gift_id = g.id AND r.user_id = $1)
			  OR EXISTS (SELECT 1 FROM gift_history h WHERE h.gift_id = g.id AND h.user_id = $1))
		)
	`, userID, giftID, photoID).Scan(&visible)
	if err == nil && !visible {
		return errNotFound
	}
	return err
}

func CheckGuestGiftPhoto(ctx context.Context, tx pgx.Tx, userID, listID, giftID int64, photoID string) error {
	return giftPhoto(ctx, tx, userID, giftID, photoID, "w.id = $4", listID)
}

func giftPhoto(ctx context.Context, db queryRower, userID, giftID int64, photoID, predicate string, listKey any) error {
	var visible bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM gifts g JOIN wishlists w ON w.id = g.wishlist_id
			WHERE g.id = $2 AND $3 = ANY(g.photo_ids) AND `+predicate+`
			  AND (w.owner_id = $1 OR `+openForPhotos+`)
			  AND CASE WHEN w.archived_at IS NOT NULL THEN g.visible_in_archive
			           ELSE g.active OR (w.owner_id <> $1 AND EXISTS (
			               SELECT 1 FROM reservations WHERE gift_id = g.id AND user_id = $1
			           )) END
		)
	`, userID, giftID, photoID, listKey).Scan(&visible)
	if err == nil && !visible {
		return errNotFound
	}
	return err
}

const openForPhotos = `(w.archived_at IS NULL AND (w.close_at IS NULL OR clock_timestamp() < w.close_at))`

func (s Store) OwnAvatar(ctx context.Context, userID, listID int64, photoID string) error {
	return checkAvatar(ctx, s.DB, photoID, "w.id = $2 AND w.owner_id = $3", listID, userID)
}

func (s Store) SharedAvatar(ctx context.Context, userID int64, token, photoID string) error {
	return checkAvatar(ctx, s.DB, photoID, "w.share_token = $2 AND (w.owner_id = $3 OR "+openForPhotos+")", token, userID)
}

func CheckGuestAvatar(ctx context.Context, tx pgx.Tx, listID int64, photoID string) error {
	return checkAvatar(ctx, tx, photoID, "w.id = $2 AND "+openForPhotos, listID)
}

func checkAvatar(ctx context.Context, db queryRower, photoID, predicate string, args ...any) error {
	var visible bool
	err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wishlists w WHERE w.avatar_id = $1 AND `+predicate+`)`,
		append([]any{photoID}, args...)...).Scan(&visible)
	if err == nil && !visible {
		return errNotFound
	}
	return err
}

func (s Store) SetAvatar(ctx context.Context, ownerID, listID int64, photoID *string, upload photos.Upload) error {
	return s.withOwnedListLocked(ctx, ownerID, listID, func(tx pgx.Tx, l list, now time.Time) error {
		if err := l.requireEditing(now); err != nil {
			return err
		}
		if photoID != nil {
			if err := upload(ctx, tx); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, "UPDATE wishlists SET avatar_id = $2 WHERE id = $1", l.ID, photoID)
		return err
	})
}

func (s Store) RemoveUnusedPhotos(ctx context.Context, remove func(context.Context, string) error) (int, error) {
	// Allow saves to finish before considering a new object. Only new uploads can
	// attach a previously unreferenced ID. Copies reuse IDs from immutable archived lists.
	cutoff, err := dbTime(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	var failures error
	count := 0
	after := ""
	for {
		rows, err := s.DB.Query(ctx, `
		SELECT p.id FROM gift_photo_objects p
		WHERE p.created_at < $1::timestamptz - interval '1 hour' AND p.id > $2
		  AND NOT EXISTS (SELECT 1 FROM gifts WHERE photo_ids @> ARRAY[p.id])
		  AND NOT EXISTS (SELECT 1 FROM users WHERE avatar_id = p.id)
		  AND NOT EXISTS (SELECT 1 FROM wishlists WHERE avatar_id = p.id)
		ORDER BY p.id LIMIT 100
	`, cutoff, after)
		if err != nil {
			return count, errors.Join(failures, err)
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return count, errors.Join(failures, err)
		}
		for _, id := range ids {
			deleted := false
			err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
				var locked string
				err := tx.QueryRow(ctx, `SELECT id FROM gift_photo_objects WHERE id = $1 FOR UPDATE SKIP LOCKED`, id).Scan(&locked)
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				if err != nil {
					return err
				}
				var referenced bool
				err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM gifts WHERE photo_ids @> ARRAY[$1::text])
				OR EXISTS (SELECT 1 FROM users WHERE avatar_id = $1)
				OR EXISTS (SELECT 1 FROM wishlists WHERE avatar_id = $1)`, id).Scan(&referenced)
				if err != nil || referenced {
					return err
				}
				if err := remove(ctx, id); err != nil {
					return err
				}
				_, err = tx.Exec(ctx, `DELETE FROM gift_photo_objects WHERE id = $1`, id)
				deleted = err == nil
				return err
			})
			if err != nil {
				failures = errors.Join(failures, err)
				if ctx.Err() != nil {
					return count, failures
				}
			} else if deleted {
				count++
			}
		}
		if len(ids) < 100 {
			return count, failures
		}
		after = ids[len(ids)-1]
	}
}
