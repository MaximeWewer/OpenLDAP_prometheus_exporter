package security

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// Constants for security configuration
const (
	// Rate limiting constants
	DefaultCleanupInterval = 10 * time.Minute
	DefaultRetryAfter      = 60

	// Validation constants
	MaxDNLength        = 8192
	MaxFilterLength    = 2048
	MaxAttributeLength = 256
)

// RateLimitMonitoring defines the interface for rate limiting monitoring
type RateLimitMonitoring interface {
	RecordRateLimitRequest(clientIP, endpoint string)
	RecordRateLimitBlocked(clientIP, endpoint string)
}

// RateLimiter implements a simple token bucket rate limiter per IP address
type RateLimiter struct {
	clients         map[string]*clientBucket
	mutex           sync.RWMutex
	rate            int           // requests per minute
	burst           int           // maximum burst size
	cleanupInterval time.Duration // cleanup interval for old clients
	stopCh          chan struct{} // channel to stop cleanup goroutine
	stopped         bool          // flag to indicate if rate limiter is stopped
}

// clientBucket represents the token bucket for a specific client IP.
// tokens is kept as float64 so accrual below one-token-per-interval
// accumulates across calls instead of being truncated to zero on every
// Allow() — the previous int accounting (int(elapsed.Minutes()*rate))
// silently dropped fractions, making a rate of 60/min effectively degrade
// to burst-only service whenever requests arrived faster than one per
// second.
type clientBucket struct {
	tokens   float64
	lastSeen time.Time
	mutex    sync.Mutex
}

// NewRateLimiter creates a new rate limiter with specified rate and burst
func NewRateLimiter(requestsPerMinute, burstSize int) *RateLimiter {
	rl := &RateLimiter{
		clients:         make(map[string]*clientBucket),
		rate:            requestsPerMinute,
		burst:           burstSize,
		cleanupInterval: DefaultCleanupInterval,
		stopCh:          make(chan struct{}),
		stopped:         false,
	}

	// Start cleanup goroutine
	go rl.cleanupRoutine()

	return rl
}

// Stop gracefully stops the rate limiter and its cleanup goroutine
func (rl *RateLimiter) Stop() {
	rl.mutex.Lock()
	if !rl.stopped {
		rl.stopped = true
		close(rl.stopCh)
	}
	rl.mutex.Unlock()
}

// Allow checks if a request from the given IP is allowed
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mutex.RLock()
	bucket, exists := rl.clients[ip]
	rl.mutex.RUnlock()

	if !exists {
		// Create new bucket for this IP
		rl.mutex.Lock()
		// Double-check after acquiring write lock
		if bucket, exists = rl.clients[ip]; !exists {
			bucket = &clientBucket{
				tokens:   float64(rl.burst),
				lastSeen: time.Now(),
			}
			rl.clients[ip] = bucket
		}
		rl.mutex.Unlock()
	}

	return rl.allowBucket(bucket)
}

// allowBucket checks if the specific bucket allows a request
func (rl *RateLimiter) allowBucket(bucket *clientBucket) bool {
	bucket.mutex.Lock()
	defer bucket.mutex.Unlock()

	now := time.Now()

	// Refill the bucket with fractional precision: a request spaced
	// under one-token-interval from the previous call now adds the
	// proportional fraction instead of zero, so sustained traffic
	// actually converges to the configured rate.
	elapsed := now.Sub(bucket.lastSeen).Minutes()
	if elapsed > 0 {
		bucket.tokens += elapsed * float64(rl.rate)
		burst := float64(rl.burst)
		if bucket.tokens > burst {
			bucket.tokens = burst
		}
	}
	bucket.lastSeen = now

	if bucket.tokens >= 1 {
		bucket.tokens--
		return true
	}
	return false
}

// cleanupRoutine removes inactive clients periodically
func (rl *RateLimiter) cleanupRoutine() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanupClients()
		case <-rl.stopCh:
			return
		}
	}
}

// cleanupClients removes clients that haven't been seen for a while
func (rl *RateLimiter) cleanupClients() {
	cutoff := time.Now().Add(-rl.cleanupInterval)
	var toDelete []string

	// First pass: identify clients to delete (avoid nested locks)
	rl.mutex.RLock()
	for ip, bucket := range rl.clients {
		bucket.mutex.Lock()
		if bucket.lastSeen.Before(cutoff) {
			toDelete = append(toDelete, ip)
		}
		bucket.mutex.Unlock()
	}
	rl.mutex.RUnlock()

	// Second pass: delete identified clients
	if len(toDelete) > 0 {
		rl.mutex.Lock()
		for _, ip := range toDelete {
			// Double check as client might have been active between the two passes
			if bucket, exists := rl.clients[ip]; exists {
				bucket.mutex.Lock()
				if bucket.lastSeen.Before(cutoff) {
					delete(rl.clients, ip)
				}
				bucket.mutex.Unlock()
			}
		}
		rl.mutex.Unlock()
	}
}

// trustedProxies holds the set of CIDR ranges from which X-Forwarded-For
// and X-Real-IP headers are trusted. It is empty by default, which means
// GetClientIP ignores those headers entirely — without a configured
// trust list any anonymous client could spoof its address and rotate
// per-IP rate-limit buckets at will.
//
// Configure via SetTrustedProxies at startup, typically from the
// HTTP_TRUSTED_PROXIES environment variable.
var (
	trustedProxies   []*net.IPNet
	trustedProxiesMu sync.RWMutex
)

// SetTrustedProxies replaces the list of CIDR ranges from which proxy
// headers are trusted. Invalid entries are logged and skipped. Passing
// an empty slice (or a slice containing only invalid entries) disables
// proxy-header trust entirely, which is the secure default.
func SetTrustedProxies(cidrs []string) {
	parsed := make([]*net.IPNet, 0, len(cidrs))
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		// Allow bare IPs by converting them to /32 (IPv4) or /128 (IPv6).
		if !strings.Contains(raw, "/") {
			if ip := net.ParseIP(raw); ip != nil {
				if ip.To4() != nil {
					raw += "/32"
				} else {
					raw += "/128"
				}
			}
		}
		_, ipnet, err := net.ParseCIDR(raw)
		if err != nil {
			logger.SafeWarn("ratelimit", "Invalid trusted proxy CIDR, ignored", map[string]interface{}{
				"cidr":  raw,
				"error": err.Error(),
			})
			continue
		}
		parsed = append(parsed, ipnet)
	}

	trustedProxiesMu.Lock()
	trustedProxies = parsed
	trustedProxiesMu.Unlock()

	if len(parsed) == 0 {
		logger.SafeInfo("ratelimit", "No trusted proxies configured; X-Forwarded-For and X-Real-IP headers will be ignored")
	} else {
		logger.SafeInfo("ratelimit", "Trusted proxies configured", map[string]interface{}{
			"count": len(parsed),
		})
	}
}

// isTrustedProxy reports whether the given IP belongs to one of the
// configured trusted proxy CIDRs.
func isTrustedProxy(ip net.IP) bool {
	if ip == nil {
		return false
	}
	trustedProxiesMu.RLock()
	defer trustedProxiesMu.RUnlock()
	for _, ipnet := range trustedProxies {
		if ipnet.Contains(ip) {
			return true
		}
	}
	return false
}

// remoteAddrIP parses r.RemoteAddr (which may or may not carry a port)
// and returns the underlying net.IP, or nil if it cannot be parsed.
func remoteAddrIP(r *http.Request) net.IP {
	if r == nil || r.RemoteAddr == "" {
		return nil
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	return net.ParseIP(strings.TrimSpace(host))
}

// GetClientIP extracts the real client IP from the request. It only
// honors X-Forwarded-For and X-Real-IP when r.RemoteAddr is itself the
// address of a configured trusted proxy — otherwise any client could
// spoof its source address and defeat the per-IP rate limiter.
func GetClientIP(r *http.Request) string {
	remote := remoteAddrIP(r)

	// Only consult proxy headers when the request came from a trusted
	// source. Without this gate, an attacker sets X-Forwarded-For: 1.2.3.4
	// from anywhere on the internet and gets a fresh rate-limit bucket.
	if remote != nil && isTrustedProxy(remote) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Left-most non-proxy IP wins, per convention — walk from
			// left and skip entries that are themselves trusted proxies.
			ips := strings.Split(xff, ",")
			for _, ipStr := range ips {
				cleanIP := strings.TrimSpace(ipStr)
				ip := net.ParseIP(cleanIP)
				if ip == nil {
					continue
				}
				if !isTrustedProxy(ip) {
					return ip.String()
				}
			}
			// All entries are trusted proxies → fall through to X-Real-IP
			// or RemoteAddr rather than returning a proxy address.
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			if ip := net.ParseIP(strings.TrimSpace(xri)); ip != nil {
				return ip.String()
			}
		}
	}

	// Fall back to the direct peer address.
	if r.RemoteAddr == "" {
		return "127.0.0.1"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		if ip := net.ParseIP(strings.TrimSpace(r.RemoteAddr)); ip != nil {
			return ip.String()
		}
		trimmedAddr := strings.TrimSpace(r.RemoteAddr)
		if trimmedAddr != "" && trimmedAddr != "invalid" {
			return trimmedAddr
		}
		return "127.0.0.1"
	}
	return host
}

// isPrivateIP checks if an IP address is in a private range
func isPrivateIP(ip net.IP) bool {
	// Private IPv4 ranges: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
	// Loopback: 127.0.0.0/8
	// Link-local: 169.254.0.0/16
	privateIPv4Ranges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
	}

	for _, cidr := range privateIPv4Ranges {
		_, subnet, _ := net.ParseCIDR(cidr)
		if subnet.Contains(ip) {
			return true
		}
	}

	// Check IPv6 private ranges
	if ip.To4() == nil { // IPv6
		// Private IPv6 ranges: fc00::/7, ::1/128
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return true
		}
		// Unique local addresses (fc00::/7)
		if len(ip) == 16 && (ip[0]&0xfe) == 0xfc {
			return true
		}
	}

	return false
}

// GetStats returns rate limiter statistics
func (rl *RateLimiter) GetStats() map[string]interface{} {
	rl.mutex.RLock()
	defer rl.mutex.RUnlock()

	return map[string]interface{}{
		"client_count": len(rl.clients),
		"rate":         rl.rate,
		"burst":        rl.burst,
	}
}

// Reset resets the rate limiter state for a given identifier
func (rl *RateLimiter) Reset(identifier string) {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	delete(rl.clients, identifier)
}

// ResetAll resets all rate limiter state
func (rl *RateLimiter) ResetAll() {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	rl.clients = make(map[string]*clientBucket)
}

// RateLimitMiddleware creates an HTTP middleware that applies rate limiting
func RateLimitMiddleware(limiter *RateLimiter) func(http.Handler) http.Handler {
	return RateLimitMiddlewareWithMonitoring(limiter, nil)
}

// RateLimitMiddlewareWithMonitoring creates an HTTP middleware that applies rate limiting with monitoring
func RateLimitMiddlewareWithMonitoring(limiter *RateLimiter, monitoring RateLimitMonitoring) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := GetClientIP(r)

			// Record rate limit request
			if monitoring != nil {
				monitoring.RecordRateLimitRequest(clientIP, r.URL.Path)
			}

			if !limiter.Allow(clientIP) {
				// Record blocked request
				if monitoring != nil {
					monitoring.RecordRateLimitBlocked(clientIP, r.URL.Path)
				}

				logger.SafeWarn("ratelimit", "Rate limit exceeded", map[string]interface{}{
					"client_ip": clientIP,
					"endpoint":  r.URL.Path,
					"method":    r.Method,
				})

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "60") // Suggest retry after 1 minute
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"Rate limit exceeded","retry_after":"60s"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
