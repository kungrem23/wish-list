package wishlists

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"wishlist/internal/history"
)

func (s Store) ArchiveDue(ctx context.Context) (int, error) {
	cutoff, err := dbTime(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	const batchSize = 100
	type candidate struct {
		ID      int64
		CloseAt time.Time
	}
	count := 0
	var firstFailure error
	failed := 0
	var after *time.Time
	var afterID int64
	for {
		rows, err := s.DB.Query(ctx, `
			SELECT id, close_at FROM wishlists
			WHERE archived_at IS NULL AND close_at <= $1
			  AND (close_at, id) > (COALESCE($2, '-infinity'::timestamptz), $3)
			ORDER BY close_at, id LIMIT $4
		`, cutoff, after, afterID, batchSize)
		if err != nil {
			return count, errors.Join(archiveFailures(failed, firstFailure), err)
		}
		batch, err := pgx.CollectRows(rows, pgx.RowToStructByPos[candidate])
		if err != nil {
			return count, errors.Join(archiveFailures(failed, firstFailure), err)
		}
		for _, item := range batch {
			archived := false
			err := s.withLocked(ctx, "id = $1", []any{item.ID}, func(tx pgx.Tx, l list, now time.Time) error {
				if l.ArchivedAt != nil || l.CloseAt == nil || now.Before(*l.CloseAt) {
					return nil
				}
				if err := archive(ctx, tx, l.ID); err != nil {
					return err
				}
				archived = true
				return nil
			})
			if err != nil {
				failed++
				if firstFailure == nil {
					firstFailure = fmt.Errorf("archive wishlist %d: %w", item.ID, err)
				}
				if ctx.Err() != nil {
					return count, archiveFailures(failed, firstFailure)
				}
			} else if archived {
				count++
			}
		}
		if len(batch) < batchSize {
			return count, archiveFailures(failed, firstFailure)
		}
		last := batch[len(batch)-1]
		after, afterID = &last.CloseAt, last.ID
	}
}

func (s Store) Archive(ctx context.Context, ownerID, id int64) error {
	return s.withOwnedListLocked(ctx, ownerID, id, func(tx pgx.Tx, l list, now time.Time) error {
		if l.ArchivedAt != nil {
			return nil
		}
		if !l.open(now) {
			return errArchive
		}
		return archive(ctx, tx, l.ID)
	})
}

func archiveFailures(count int, first error) error {
	if count <= 1 {
		return first
	}
	return fmt.Errorf("%d wishlist archive failures (first: %w)", count, first)
}

func archive(ctx context.Context, tx pgx.Tx, id int64) error {
	// At READ COMMITTED this statement sees writes committed while the
	// archiver was waiting on the wishlist row.
	_, err := tx.Exec(ctx, `
		UPDATE gifts g SET visible_in_archive = g.active OR outcome.reserved, gifted = outcome.reserved
		FROM (
			SELECT card.id, r.gift_id IS NOT NULL AS reserved
			FROM gifts card LEFT JOIN reservations r ON r.gift_id = card.id WHERE card.wishlist_id = $1
		) outcome
		WHERE g.id = outcome.id
	`, id)
	if err != nil {
		return err
	}
	if err := history.RecordReservations(ctx, tx, id); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM reservations r USING gifts g WHERE r.gift_id = g.id AND g.wishlist_id = $1`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE wishlists SET archived_at = clock_timestamp() WHERE id = $1`, id)
	return err
}
