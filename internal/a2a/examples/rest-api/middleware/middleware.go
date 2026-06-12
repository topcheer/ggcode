// Package middleware provides HTTP middleware for the REST API.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// contextKey is an unexported type for context keys defined in this package.
type contextKey string

const requestIDKey contextKey = "request_id"

// RequestIDFromContext retrieves the request ID from a context.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// RequestID injects a unique X-Request-ID into the response and context.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.New().String()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// Logging logs each request as structured JSON via slog.
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r)

			duration := time.Since(start)
			logger.Info("request completed",
				"method", r.Method,
				"path", r.URL.Path,
				"query", r.URL.RawQuery,
				"status", rw.status,
				"duration_ms", duration.Milliseconds(),
				"request_id", RequestIDFromContext(r.Context()),
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent(),
			)
		})
	}
}

// Recovery catches panics and returns a 500 error.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered",
						"error", err,
						"method", r.Method,
						"path", r.URL.Path,
						"request_id", RequestIDFromContext(r.Context()),
					)
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"success":false,"error":{"code":"INTERNAL_ERROR","message":"an internal error occurred"}}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// CORS adds Cross-Origin Resource Sharing headers.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	origins := strings.Join(allowedOrigins, ", ")
	if origins == "" {
		origins = "*"
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", origins)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
			w.Header().Set("Access-Control-Max-Age", "300")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimiter provides a simple token-bucket rate limiter per IP.
type RateLimiter struct {
	mu        sync.Mutex
	clients   map[string]*clientBucket
	rate      int           // max requests per window
	window    time.Duration // time window
	cleanup   time.Duration // cleanup interval
	lastSweep time.Time
}

type clientBucket struct {
	count   int
	resetAt time.Time
}

// NewRateLimiter creates a rate limiter allowing `rate` requests per `window`.
func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		clients:   make(map[string]*clientBucket),
		rate:      rate,
		window:    window,
		cleanup:   window * 2,
		lastSweep: time.Now(),
	}
}

// Middleware returns an HTTP middleware that enforces rate limiting.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.RemoteAddr

		if !rl.allow(key) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"success":false,"error":{"code":"RATE_LIMITED","message":"rate limit exceeded, try again later"}}`))
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Periodic cleanup of stale entries.
	if now.Sub(rl.lastSweep) > rl.cleanup {
		for k, v := range rl.clients {
			if now.After(v.resetAt) {
				delete(rl.clients, k)
			}
		}
		rl.lastSweep = now
	}

	bucket, exists := rl.clients[key]
	if !exists || now.After(bucket.resetAt) {
		rl.clients[key] = &clientBucket{
			count:   1,
			resetAt: now.Add(rl.window),
		}
		return true
	}

	bucket.count++
	return bucket.count <= rl.rate
}

// ContentType enforces Content-Type: application/json on write operations.
func ContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnsupportedMediaType)
				w.Write([]byte(`{"success":false,"error":{"code":"BAD_REQUEST","message":"Content-Type must be application/json"}}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
