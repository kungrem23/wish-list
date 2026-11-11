package history

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	DB *pgxpool.Pool
}

type Entry struct {
	GiftID     int64     `json:"gift_id,string"`
	GiftTitle  string    `json:"gift_title"`
	Wishlist   string    `json:"wishlist_title"`
	Recipient  string    `json:"recipient"`
	ReservedAt time.Time `json:"reserved_at"`
	Gifted     bool      `json:"gifted"`
}

// RecordReservations uses the archiving transaction so all lifecycle changes commit together.
func RecordReservations(ctx context.Context, tx pgx.Tx, wishlistID int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO gift_history (gift_id, user_id, reserved_at)
		SELECT r.gift_id, r.user_id, r.reserved_at FROM reservations r
		JOIN gifts g ON g.id = r.gift_id WHERE g.wishlist_id = $1
	`, wishlistID)
	return err
}

func (s Store) List(ctx context.Context, userID int64, before *time.Time, after int64, limit int) ([]Entry, error) {
	// Gift outcomes belong to wishlists. Read the current outcome on every request.
	rows, err := s.DB.Query(ctx, `
		SELECT h.gift_id, g.title, w.title, u.display_name, h.reserved_at, g.gifted
		FROM gift_history h
		JOIN gifts g ON g.id = h.gift_id
		JOIN wishlists w ON w.id = g.wishlist_id
		JOIN users u ON u.id = w.owner_id
		WHERE h.user_id = $1
		AND (h.reserved_at, h.gift_id) < (COALESCE($2, 'infinity'::timestamptz), $3)
		ORDER BY h.reserved_at DESC, h.gift_id DESC LIMIT $4
	`, userID, before, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Entry, error) {
		var entry Entry
		err := row.Scan(&entry.GiftID, &entry.GiftTitle, &entry.Wishlist, &entry.Recipient, &entry.ReservedAt, &entry.Gifted)
		return entry, err
	})
}

func (s Store) Delete(ctx context.Context, userID, giftID int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM gift_history WHERE gift_id = $1 AND user_id = $2`, giftID, userID)
	return err
}
