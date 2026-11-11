package wishlists

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type View struct {
	Summary
	IsOwner   bool   `json:"is_owner"`
	OwnerName string `json:"owner_name"`
	Gifts     []Gift `json:"gifts"`
}

// GuestView is the wishlist data that can be shared through an independent grant.
type GuestView struct {
	Details
	CloseAt  *time.Time `json:"close_at"`
	Phase    string     `json:"phase"`
	Gifts    []Gift     `json:"-"`
	AvatarID *string    `json:"-"`
}

func CheckOwnedOpen(ctx context.Context, tx pgx.Tx, ownerID, listID int64) error {
	var l list
	err := l.scan(tx.QueryRow(ctx, `SELECT `+listColumns+` FROM wishlists WHERE id = $1 AND owner_id = $2 FOR SHARE`, listID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	now, err := dbTime(ctx, tx)
	if err == nil && !l.open(now) {
		return errNotFound
	}
	return err
}

func ReadGuestView(ctx context.Context, tx pgx.Tx, userID, listID int64, cursor GiftCursor, limit int) (GuestView, error) {
	var view GuestView
	var l list
	err := l.scan(tx.QueryRow(ctx, `SELECT `+listColumns+` FROM wishlists WHERE id = $1 FOR SHARE`, listID))
	if err != nil {
		return view, err
	}
	now, err := dbTime(ctx, tx)
	if err != nil {
		return view, err
	}
	if !l.open(now) {
		return view, errNotFound
	}
	view.Details = l.Details
	view.CloseAt = l.CloseAt
	view.AvatarID = l.AvatarID
	view.Phase, err = l.phase(now)
	if err == nil {
		view.Gifts, err = readGifts(ctx, tx, l, userID, cursor, limit)
	}
	return view, err
}

type Booking struct {
	GiftID     int64     `json:"gift_id,string"`
	GiftTitle  string    `json:"gift_title"`
	Wishlist   string    `json:"wishlist_title"`
	Recipient  string    `json:"recipient"`
	ShareToken string    `json:"share_token,omitempty"`
	ReservedAt time.Time `json:"reserved_at"`
	Active     bool      `json:"active"`
	CanCancel  bool      `json:"can_cancel"`
}

func (s Store) Owned(ctx context.Context, userID int64, archived bool, date string, after int64, limit int) ([]Summary, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT `+listColumns+`, statement_timestamp(), `+giftCount+` FROM wishlists w WHERE owner_id = $1
		AND (archived_at IS NOT NULL, COALESCE(holiday_date, '-infinity'::date), id) >
		    ($2, COALESCE(NULLIF($3, '')::date, '-infinity'::date), $4)
		ORDER BY archived_at IS NOT NULL, COALESCE(holiday_date, '-infinity'::date), id LIMIT $5
	`, userID, archived, date, after, limit)
	return collectLists(rows, err)
}

func collectLists(rows pgx.Rows, err error) ([]Summary, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Summary, error) {
		var l list
		var now time.Time
		if err := l.scan(row, &now, &l.GiftCount); err != nil {
			return Summary{}, err
		}
		l.Phase, err = l.phase(now)
		return l.Summary, err
	})
}

func (s Store) OwnView(ctx context.Context, userID, id int64, cursor GiftCursor, limit int) (View, error) {
	return s.view(ctx, userID, "id", id, true, cursor, limit)
}

func (s Store) SharedView(ctx context.Context, userID int64, token string, cursor GiftCursor, limit int) (View, error) {
	return s.view(ctx, userID, "share_token", token, false, cursor, limit)
}

func (s Store) view(ctx context.Context, userID int64, column string, key any, ownerOnly bool, cursor GiftCursor, limit int) (View, error) {
	var view View
	// Use one snapshot for the list and gifts so an archive commit cannot mix
	// the live view with archived data.
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var l list
		var now time.Time
		err := l.scan(tx.QueryRow(ctx, `
			SELECT `+listColumns+`, (SELECT display_name FROM users WHERE id = w.owner_id), statement_timestamp(), `+giftCount+`
			FROM wishlists w WHERE `+column+` = $1
		`, key), &view.OwnerName, &now, &l.GiftCount)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		view.IsOwner = userID == l.ownerID
		if !view.IsOwner && (ownerOnly || !l.open(now)) {
			return errNotFound
		}
		l.Phase, err = l.phase(now)
		if err != nil {
			return err
		}
		view.Summary = l.Summary
		view.Gifts, err = readGifts(ctx, tx, l, userID, cursor, limit)
		return err
	})
	return view, err
}

const giftCount = `(SELECT count(*) FROM gifts g WHERE g.wishlist_id = w.id
	AND CASE WHEN w.archived_at IS NULL THEN g.active ELSE g.visible_in_archive END)`

const (
	SortPriority = "priority"
	SortName     = "name"
	SortAdded    = "added"
)

type GiftCursor struct {
	Sort      string
	ID        int64
	Priority  string     `json:",omitempty"`
	Title     string     `json:",omitempty"`
	CreatedAt *time.Time `json:",omitempty"`
}

func (c GiftCursor) Next(g Gift) GiftCursor {
	next := GiftCursor{Sort: c.Sort, ID: g.ID}
	switch c.Sort {
	case SortName:
		next.Title = g.Title
	case SortAdded:
		next.CreatedAt = &g.CreatedAt
	default:
		next.Priority = g.Priority
	}
	return next
}

func readGifts(ctx context.Context, tx pgx.Tx, l list, userID int64, cursor GiftCursor, limit int) ([]Gift, error) {
	const fields = `SELECT g.id, g.title, g.description, g.product_url, g.price::text, g.priority::text,
		g.active, g.photo_ids, g.created_at, `
	status := `NULL::boolean, ''`
	source := ` FROM gifts g WHERE g.wishlist_id = $1 AND g.active`
	args := []any{l.ID, cursor.ID, limit}
	var order, boundary string
	switch cursor.Sort {
	case SortName:
		order = "g.title COLLATE wishlist_gift_name, g.id"
		boundary = "(g.title COLLATE wishlist_gift_name, g.id) > ($4::text COLLATE wishlist_gift_name, $2)"
		args = append(args, cursor.Title)
	case SortAdded:
		order = "g.created_at DESC, g.id DESC"
		boundary = "(g.created_at, g.id) < ($4::timestamptz, $2)"
		args = append(args, cursor.CreatedAt)
	default:
		order = "g.priority, g.id"
		boundary = "(g.priority, g.id) > (NULLIF($4, '')::gift_priority, $2)"
		args = append(args, cursor.Priority)
	}
	switch {
	case l.ArchivedAt != nil:
		status = `g.gifted, ''`
		source = ` FROM gifts g WHERE g.wishlist_id = $1 AND g.visible_in_archive`
	case userID != l.ownerID:
		status = `NULL::boolean, CASE WHEN r.user_id = $5 THEN 'mine' WHEN r.gift_id IS NOT NULL THEN 'taken' ELSE 'free' END`
		source = ` FROM gifts g LEFT JOIN reservations r ON r.gift_id = g.id
			WHERE g.wishlist_id = $1 AND (g.active OR r.user_id = $5)`
		args = append(args, userID)
	}
	rows, err := tx.Query(ctx, fields+status+source+` AND ($2::bigint = 0 OR `+boundary+`) ORDER BY `+order+` LIMIT $3`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Gift, error) {
		var g Gift
		err := row.Scan(&g.ID, &g.Title, &g.Description, &g.ProductURL, &g.Price, &g.Priority,
			&g.Active, &g.PhotoIDs, &g.CreatedAt, &g.Gifted, &g.Reservation)
		return g, err
	})
}

func (s Store) Bookings(ctx context.Context, userID int64, before *time.Time, after int64, limit int) ([]Booking, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT g.id, g.title, w.title, u.display_name, CASE WHEN r.via_santa OR w.archived_at IS NOT NULL OR w.close_at <= statement_timestamp()
		            THEN '' ELSE w.share_token END, r.reserved_at, g.active,
		       w.archived_at IS NULL AND (w.close_at IS NULL OR statement_timestamp() < w.close_at)
		FROM reservations r
		JOIN gifts g ON g.id = r.gift_id
		JOIN wishlists w ON w.id = g.wishlist_id
		JOIN users u ON u.id = w.owner_id
		WHERE r.user_id = $1
		AND (r.reserved_at, r.gift_id) < (COALESCE($2, 'infinity'::timestamptz), $3)
		ORDER BY r.reserved_at DESC, r.gift_id DESC LIMIT $4
	`, userID, before, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Booking, error) {
		var b Booking
		err := row.Scan(&b.GiftID, &b.GiftTitle, &b.Wishlist, &b.Recipient, &b.ShareToken, &b.ReservedAt, &b.Active, &b.CanCancel)
		return b, err
	})
}
