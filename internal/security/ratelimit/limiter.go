package ratelimit

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type bucket struct {
	start time.Time
	count int
}
type Limiter struct {
	mu             sync.Mutex
	window         time.Duration
	limit, maxKeys int
	buckets        map[string]bucket
}

func New(window time.Duration, limit, maxKeys int) *Limiter {
	if window <= 0 {
		window = time.Minute
	}
	if limit <= 0 {
		limit = 60
	}
	if maxKeys <= 0 {
		maxKeys = 10000
	}
	return &Limiter{window: window, limit: limit, maxKeys: maxKeys, buckets: make(map[string]bucket)}
}
func (l *Limiter) Allow(key string) bool {
	if key == "" {
		key = "unknown"
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.buckets {
		if now.Sub(b.start) >= l.window {
			delete(l.buckets, k)
		}
	}
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			return false
		}
		b = bucket{start: now}
	}
	if now.Sub(b.start) >= l.window {
		b = bucket{start: now}
	}
	if b.count >= l.limit {
		l.buckets[key] = b
		return false
	}
	b.count++
	l.buckets[key] = b
	return true
}
func (l *Limiter) Middleware(key func(*http.Request) string, limited func(*http.Request) bool) func(http.HandlerFunc) http.HandlerFunc {
	return l.MiddlewareWithReject(key, limited, nil)
}

func (l *Limiter) MiddlewareWithReject(key func(*http.Request) string, limited func(*http.Request) bool, onReject func()) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if limited == nil || !limited(r) {
				next(w, r)
				return
			}
			bucketKey := clientIP(r)
			if key != nil {
				if userKey := key(r); userKey != "" {
					bucketKey = "user:" + userKey
				}
			}
			if !l.Allow(bucketKey) {
				if onReject != nil {
					onReject()
				}
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int(l.window.Seconds()))))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"request limit exceeded"}}`))
				return
			}
			next(w, r)
		}
	}
}
func LimitedAPIRoute(r *http.Request) bool {
	p := r.URL.Path
	return p == "/api/v1/auth/login" || p == "/api/v1/tasks" && r.Method == http.MethodPost || strings.HasPrefix(p, "/api/v1/tasks/") && r.Method == http.MethodPost && (strings.HasSuffix(p, "/start") || strings.HasSuffix(p, "/approve") || strings.HasSuffix(p, "/execute"))
}
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return "ip:" + host
	}
	return "ip:" + r.RemoteAddr
}
