package santa

import (
	"context"

	"github.com/jackc/pgx/v5"

	"wishlist/internal/wishlists"
)

func (s Store) withParticipant(ctx context.Context, userID, id int64, action func(pgx.Tx, eventRow) error) error {
	return s.write(ctx, func(tx pgx.Tx) error {
		e, err := lockEvent(ctx, tx, "id = $1", id, "FOR UPDATE")
		if err != nil {
			return err
		}
		var member bool
		err = tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM santa_participants WHERE event_id = $1 AND user_id = $2)", id, userID).Scan(&member)
		if err != nil {
			return err
		}
		if !member {
			return errNotFound
		}
		if err := e.requireOpen(ctx, tx); err != nil {
			return err
		}
		return action(tx, e)
	})
}

func (s Store) Bind(ctx context.Context, userID, id int64, listID *int64) error {
	return s.withParticipant(ctx, userID, id, func(tx pgx.Tx, e eventRow) error {
		if listID != nil {
			if err := wishlists.CheckOwnedOpen(ctx, tx, userID, *listID); err != nil {
				return err
			}
			if err := e.requireOpen(ctx, tx); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE santa_participants SET wishlist_id = $3 WHERE event_id = $1 AND user_id = $2`, e.id, userID, listID)
		return err
	})
}

func (s Store) CreateWishlist(ctx context.Context, userID, id int64, input wishlists.Details) (wishlists.Summary, error) {
	var list wishlists.Summary
	err := s.withParticipant(ctx, userID, id, func(tx pgx.Tx, e eventRow) error {
		var err error
		list, err = wishlists.CreateInTx(ctx, tx, userID, input)
		if err != nil {
			return err
		}
		if err := e.requireOpen(ctx, tx); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE santa_participants SET wishlist_id = $3 WHERE event_id = $1 AND user_id = $2", id, userID, list.ID)
		return err
	})
	return list, err
}

type Grant struct {
	EventID    int64
	Round      int
	WishlistID int64
}

func (s Store) withGrant(ctx context.Context, userID int64, grant Grant, action func(pgx.Tx, eventRow) error) error {
	return s.write(ctx, func(tx pgx.Tx) error {
		e, err := lockEvent(ctx, tx, "id = $1", grant.EventID, "FOR SHARE")
		if err != nil {
			return err
		}
		if grant.Round != e.round {
			return errNotFound
		}
		var allowed bool
		err = tx.QueryRow(ctx, `
			SELECT clock_timestamp() < $5 AND EXISTS (
				SELECT 1 FROM santa_assignments sa
				JOIN santa_participants p ON p.event_id = sa.event_id AND p.user_id = sa.recipient_id
				WHERE sa.event_id = $1 AND sa.round = $2 AND sa.giver_id = $3 AND p.wishlist_id = $4
			)
		`, e.id, e.round, userID, grant.WishlistID, e.closeAt).Scan(&allowed)
		if err != nil {
			return err
		}
		if !allowed {
			return errNotFound
		}
		return action(tx, e)
	})
}

func (s Store) Wishlist(ctx context.Context, userID int64, grant Grant, cursor wishlists.GiftCursor, limit int) (wishlists.GuestView, error) {
	var view wishlists.GuestView
	err := s.withGrant(ctx, userID, grant, func(tx pgx.Tx, e eventRow) error {
		var err error
		view, err = wishlists.ReadGuestView(ctx, tx, userID, grant.WishlistID, cursor, limit)
		if err != nil {
			return err
		}
		return e.requireOpen(ctx, tx)
	})
	return view, err
}

func (s Store) Avatar(ctx context.Context, userID int64, grant Grant, photoID string) error {
	return s.withGrant(ctx, userID, grant, func(tx pgx.Tx, e eventRow) error {
		if err := wishlists.CheckGuestAvatar(ctx, tx, grant.WishlistID, photoID); err != nil {
			return err
		}
		return e.requireOpen(ctx, tx)
	})
}

func (s Store) Photo(ctx context.Context, userID int64, grant Grant, giftID int64, photoID string) error {
	return s.withGrant(ctx, userID, grant, func(tx pgx.Tx, e eventRow) error {
		if err := wishlists.CheckGuestGiftPhoto(ctx, tx, userID, grant.WishlistID, giftID, photoID); err != nil {
			return err
		}
		return e.requireOpen(ctx, tx)
	})
}

func (s Store) Reserve(ctx context.Context, userID int64, grant Grant, giftID int64) error {
	return s.withGrant(ctx, userID, grant, func(tx pgx.Tx, e eventRow) error {
		return wishlists.ReserveFromEvent(ctx, tx, userID, grant.WishlistID, giftID, e.closeAt)
	})
}

func (s Store) Cancel(ctx context.Context, userID int64, grant Grant, giftID int64) error {
	return s.withGrant(ctx, userID, grant, func(tx pgx.Tx, e eventRow) error {
		return wishlists.CancelFromEvent(ctx, tx, userID, grant.WishlistID, giftID, e.closeAt)
	})
}
