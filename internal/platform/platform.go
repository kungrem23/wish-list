package platform

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func OpenDB(ctx context.Context) (*pgxpool.Pool, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		// Driver diagnostics may include the original connection string.
		return nil, errors.New("invalid DATABASE_URL configuration")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("could not connect to PostgreSQL")
	}
	return pool, nil
}

func Token() string {
	var data [32]byte
	rand.Read(data[:])
	return base64.RawURLEncoding.EncodeToString(data[:])
}
