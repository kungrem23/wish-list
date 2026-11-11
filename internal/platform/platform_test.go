package platform

import (
	"context"
	"testing"
)

func TestOpenDBReportsSafeConfigurationError(t *testing.T) {
	t.Setenv("DATABASE_URL", "host=127.0.0.1 port=notaport user=wishlist password = 'synthetic sentinel'")
	pool, err := OpenDB(context.Background())
	if pool != nil {
		pool.Close()
		t.Fatal("expected invalid configuration to fail")
	}
	if err == nil || err.Error() != "invalid DATABASE_URL configuration" {
		t.Fatal("expected a redacted configuration error")
	}
}
