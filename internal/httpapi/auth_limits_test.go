package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestAuthLimitsAtHTTPRoutes(t *testing.T) {
	handler := Handler(nil, true)
	call := func(path, ip, email, password string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
		if path == "register" {
			body = fmt.Sprintf(`{"email":%q,"password":%q,"display_name":"Test"}`, email, password)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/"+path, strings.NewReader(body))
		request.RemoteAddr = ip + ":1234"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Real-IP", email)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	assert := func(w *httptest.ResponseRecorder, want int) {
		t.Helper()
		if w.Code != want || (want == 429 && w.Header().Get("Retry-After") == "") {
			t.Fatalf("want %d, got %d: %s", want, w.Code, w.Body)
		}
	}

	tooLong := strings.Repeat("x", 73) // Login rejects this before contacting the database.
	weak := call("register", "192.0.2.1", "test@example.org", "PASSWORD")
	assert(weak, 400)
	if !strings.Contains(weak.Body.String(), "слишком распространён") {
		t.Fatal(weak.Body)
	}
	for range authEmailAttempts - 1 {
		assert(call("login", "192.0.2.2", "TEST@EXAMPLE.ORG", tooLong), 401)
	}
	assert(call("login", "192.0.2.3", " test@example.org ", tooLong), 429)

	for i := range authIPAttempts {
		assert(call("login", "192.0.2.4", fmt.Sprintf("test%d@example.org", i), tooLong), 401)
	}
	assert(call("register", "192.0.2.4", "other@example.org", "PASSWORD"), 429)
	assert(call("register", "192.0.2.5", "other@example.org", "PASSWORD"), 400)
}

func TestClientKeyTrustsOnlyConfiguredProxy(t *testing.T) {
	l := newAuthLimits(netip.MustParseAddr("127.0.0.1"))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/login", nil)
	r.Header.Set("X-Real-IP", "2001:db8:1234:5678::99")
	r.RemoteAddr = "192.0.2.5:1234"
	if key := l.clientKey(r); key != "192.0.2.5" {
		t.Fatalf("direct client used a forwarded IP: %s", key)
	}
	r.RemoteAddr = "127.0.0.1:1234"
	if key := l.clientKey(r); key != "2001:db8:1234:5678::/64" {
		t.Fatalf("unexpected IPv6 client key: %s", key)
	}
	r.Header.Add("X-Real-IP", "192.0.2.5")
	if key := l.clientKey(r); key != "127.0.0.1" {
		t.Fatalf("ambiguous header changed the client key: %s", key)
	}
}

func TestAuthCapacityAndWindowRecovery(t *testing.T) {
	l := newAuthLimits(netip.Addr{})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/login", nil)
	w := httptest.NewRecorder()
	for i := range cap(l.slots) {
		if err := l.acquire(w, r, fmt.Sprintf("test%d@example.org", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.acquire(w, r, "busy@example.org"); err != errAuthBusy || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("auth capacity did not reject the excess request: %v", err)
	}
	l.release()
	if err := l.acquire(w, r, "busy@example.org"); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	limit := authAttemptLimit{attempts: 1, keys: make(map[string]authAttempts)}
	for i := range maxAuthKeys {
		if wait := limit.take(fmt.Sprint(i), now); wait != 0 {
			t.Fatalf("unexpected wait: %s", wait)
		}
	}
	if wait := limit.take("new", now); wait != authWindow {
		t.Fatalf("bounded limiter did not reject a new key: %s", wait)
	}
	if wait := limit.take("new", now.Add(authWindow)); wait != 0 || len(limit.keys) != 1 {
		t.Fatalf("expired buckets were not cleared: wait=%s keys=%d", wait, len(limit.keys))
	}
}
