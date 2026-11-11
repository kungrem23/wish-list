package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wishlist/internal/photos"
	"wishlist/internal/platform"
	"wishlist/internal/wishlists"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := platform.OpenDB(startup)
	if err != nil {
		return err
	}
	defer db.Close()

	store := wishlists.Store{DB: db}
	count, archiveErr := store.ArchiveDue(ctx)
	log.Printf("Archived wishlists: %d", count)
	photoStore, err := photos.FromEnv()
	if err != nil || photoStore == nil {
		return errors.Join(archiveErr, err)
	}
	removed, err := store.RemoveUnusedPhotos(ctx, photoStore.Delete)
	log.Printf("Removed unused gift photos: %d", removed)
	return errors.Join(archiveErr, err)
}
