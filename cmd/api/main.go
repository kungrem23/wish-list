package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wishlist/internal/httpapi"
	"wishlist/internal/photos"
	"wishlist/internal/platform"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	var trustedProxy netip.Addr
	if value := os.Getenv("TRUSTED_PROXY_IP"); value != "" {
		ip, err := netip.ParseAddr(value)
		if err != nil || ip.Zone() != "" {
			return errors.New("TRUSTED_PROXY_IP must be an IP address without a zone")
		}
		trustedProxy = ip
	}
	photoStore, err := photos.FromEnv()
	if err != nil {
		return err
	}

	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := platform.OpenDB(startup)
	if err != nil {
		return err
	}
	defer db.Close()

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           httpapi.HandlerWithPhotos(db, os.Getenv("COOKIE_SECURE") != "false", trustedProxy, photoStore),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      25 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	done := make(chan error, 1)
	go func() {
		log.Printf("Wishlist API listening on %s", addr)
		done <- server.ListenAndServe()
	}()
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
