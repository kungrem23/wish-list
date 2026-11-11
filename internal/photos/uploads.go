package photos

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wishlist/internal/problem"
)

type Upload func(context.Context, pgx.Tx) error

type UploadObject struct {
	ID   string
	Size int64
}

const (
	MaxUserBytes      int64 = 250 << 20
	MaxStorageBytes   int64 = 5 << 30
	MaxUserObjects          = 1000
	MaxStorageObjects       = 20000
	MaxHourlyUploads        = 60
)

// TrackUploads reserves space durably before S3 writes, after the referencing
// transaction has authorized and validated the change. Failed writes stay charged
// until cleanup, so retries and concurrent requests cannot bypass the quota.
func TrackUploads(ctx context.Context, db *pgxpool.Pool, owner int64, objects []UploadObject) error {
	if len(objects) == 0 {
		return nil
	}
	var added int64
	for _, object := range objects {
		if object.Size < 1 || object.Size > 5<<20 {
			return fmt.Errorf("invalid photo size")
		}
		added += object.Size
	}
	// Referencing transactions already hold pool connections. A short-lived,
	// separate connection prevents pool exhaustion from deadlocking all uploads.
	// HTTP upload concurrency bounds the number of these extra connections.
	conn, err := pgx.ConnectConfig(ctx, db.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '3s'"); err != nil {
			return err
		}
		// A separate namespace from the migration lock. Shared by every API process.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(2079153101)"); err != nil {
			return err
		}
		var allBytes, userBytes int64
		var allCount, userCount, hourly int
		err := tx.QueryRow(ctx, `SELECT COALESCE(sum(size_bytes), 0), count(*),
		    COALESCE(sum(size_bytes) FILTER (WHERE owner_id = $1), 0),
		    count(*) FILTER (WHERE owner_id = $1),
		    count(*) FILTER (WHERE owner_id = $1 AND created_at > statement_timestamp() - interval '1 hour')
		    FROM gift_photo_objects`, owner).Scan(&allBytes, &allCount, &userBytes, &userCount, &hourly)
		if err != nil {
			return err
		}
		if hourly+len(objects) > MaxHourlyUploads {
			return problem.New(http.StatusTooManyRequests, "Не больше 60 фотографий в час. Повторите позже")
		}
		if userBytes+added > MaxUserBytes || userCount+len(objects) > MaxUserObjects {
			return problem.New(http.StatusConflict, "Достигнут лимит хранилища фотографий аккаунта. Удалите лишние фотографии и дождитесь очистки")
		}
		if allBytes+added > MaxStorageBytes || allCount+len(objects) > MaxStorageObjects {
			return problem.New(http.StatusServiceUnavailable, "Хранилище фотографий заполнено")
		}
		for _, object := range objects {
			if _, err := tx.Exec(ctx, "INSERT INTO gift_photo_objects (id, owner_id, size_bytes) VALUES ($1, $2, $3)", object.ID, owner, object.Size); err != nil {
				return err
			}
		}
		return nil
	})
}

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
