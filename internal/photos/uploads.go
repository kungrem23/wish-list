package photos

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LockUploads protects a newly tracked object until the referencing transaction commits.
func LockUploads(ctx context.Context, tx pgx.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id FROM gift_photo_objects WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	locked, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err == nil && len(locked) != len(ids) {
		return fmt.Errorf("could not lock all tracked photo uploads")
	}
	return err
}
