package wishlists

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"wishlist/internal/accounts"
)

type FriendProfile struct {
	accounts.PublicProfile
	Lists []Summary `json:"active_lists"`
}

func (s Store) FriendProfile(ctx context.Context, viewerID, targetID int64, token, date string, after int64, limit int) (FriendProfile, error) {
	profile := FriendProfile{Lists: []Summary{}}
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if token != "" {
			err := tx.QueryRow(ctx, `
				SELECT owner_id FROM wishlists WHERE share_token = $1
				AND (owner_id = $2 OR (archived_at IS NULL AND (close_at IS NULL OR close_at > transaction_timestamp())))
			`, token, viewerID).Scan(&targetID)
			if errors.Is(err, pgx.ErrNoRows) {
				return errNotFound
			}
			if err != nil {
				return err
			}
		}
		var err error
		profile.PublicProfile, err = accounts.ReadPublicProfile(ctx, tx, viewerID, targetID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT `+listColumns+`, transaction_timestamp(), `+giftCount+` FROM wishlists w
			WHERE owner_id = $1 AND archived_at IS NULL AND (close_at IS NULL OR close_at > transaction_timestamp())
			AND (COALESCE(holiday_date, '-infinity'::date), id) > (COALESCE(NULLIF($2, '')::date, '-infinity'::date), $3)
			ORDER BY COALESCE(holiday_date, '-infinity'::date), id LIMIT $4
		`, targetID, date, after, limit)
		profile.Lists, err = collectLists(rows, err)
		return err
	})
	return profile, err
}
