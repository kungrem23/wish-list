package httpapi

import (
	"crypto/sha256"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"strconv"
	"sync"
	"time"

	"wishlist/internal/accounts"
	"wishlist/internal/problem"
)

const (
	authWindow        = time.Minute
	authIPAttempts    = 30
	authEmailAttempts = 5
	maxAuthKeys       = 16_384
)

var (
	errAuthRate = problem.New(http.StatusTooManyRequests, "Слишком много попыток. Повторите позже")
	errAuthBusy = problem.New(http.StatusServiceUnavailable, "Сервис входа временно занят. Повторите попытку")
)

type authLimits struct {
	trustedProxy netip.Addr
	ips          authAttemptLimit
	emails       authAttemptLimit
	slots        chan struct{}
}

func newAuthLimits(trustedProxy netip.Addr) *authLimits {
	return &authLimits{
		trustedProxy: trustedProxy.Unmap(),
		ips:          authAttemptLimit{attempts: authIPAttempts, keys: make(map[string]authAttempts)},
		emails:       authAttemptLimit{attempts: authEmailAttempts, keys: make(map[string]authAttempts)},
		// Leave CPU capacity for other API endpoints during password checks.
		slots: make(chan struct{}, max(1, min(4, runtime.GOMAXPROCS(0)/2))),
	}
}

func (l *authLimits) byIP(next endpoint) endpoint {
	return func(w http.ResponseWriter, r *http.Request) error {
		if wait := l.ips.take(l.clientKey(r), time.Now()); wait > 0 {
			setRetryAfter(w, wait)
			return errAuthRate
		}
		return next(w, r)
	}
}

func (l *authLimits) clientKey(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip, _ := netip.ParseAddr(host)
	ip = ip.WithZone("").Unmap()
	if l.trustedProxy.IsValid() && ip == l.trustedProxy {
		realIP := r.Header.Values("X-Real-IP")
		if len(realIP) == 1 {
			if client, err := netip.ParseAddr(realIP[0]); err == nil && client.Zone() == "" {
				ip = client.Unmap()
			}
		}
	}
	if ip.Is6() {
		return netip.PrefixFrom(ip, 64).Masked().String()
	}
	return ip.String()
}

func (l *authLimits) acquire(w http.ResponseWriter, r *http.Request, email string) error {
	if err := r.Context().Err(); err != nil {
		return err
	}
	select {
	case l.slots <- struct{}{}:
	default:
		setRetryAfter(w, time.Second)
		return errAuthBusy
	}

	key := sha256.Sum256([]byte(accounts.NormalizeEmail(email)))
	if wait := l.emails.take(string(key[:]), time.Now()); wait > 0 {
		l.release()
		setRetryAfter(w, wait)
		return errAuthRate
	}
	return nil
}

func (l *authLimits) release() { <-l.slots }

func setRetryAfter(w http.ResponseWriter, wait time.Duration) {
	seconds := (wait + time.Second - 1) / time.Second
	w.Header().Set("Retry-After", strconv.Itoa(int(seconds)))
}

type authAttempts struct {
	count   int
	expires time.Time
}

type authAttemptLimit struct {
	mu        sync.Mutex
	attempts  int
	keys      map[string]authAttempts
	nextSweep time.Time
}

func (l *authAttemptLimit) take(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !now.Before(l.nextSweep) {
		for key, entry := range l.keys {
			if !now.Before(entry.expires) {
				delete(l.keys, key)
			}
		}
		l.nextSweep = now.Add(authWindow)
	}

	entry, exists := l.keys[key]
	if !exists && len(l.keys) >= maxAuthKeys {
		return l.nextSweep.Sub(now)
	}
	if !now.Before(entry.expires) {
		entry = authAttempts{expires: now.Add(authWindow)}
	}
	if entry.count >= l.attempts {
		return entry.expires.Sub(now)
	}
	entry.count++
	l.keys[key] = entry
	return 0
}
