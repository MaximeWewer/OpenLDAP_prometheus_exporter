package security

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// RateLimiter implements a simple token bucket rate limiter per IP address
type RateLimiter struct {
	clients         map[string]*clientBucket
	mutex           sync.RWMutex
	rate            int           // requests per minute
	burst           int           // maximum burst size
	cleanupInterval time.Duration // cleanup interval for old clients
}

// clientBucket represents the token bucket for a specific client IP
type clientBucket struct {
	tokens   int
	lastSeen time.Time
	mutex    sync.Mutex
}

// NewRateLimiter creates a new rate limiter with specified rate and burst
func NewRateLimiter(requestsPerMinute, burstSize int) *RateLimiter {
	rl := &RateLimiter{
		clients:         make(map[string]*clientBucket),
		rate:            requestsPerMinute,
		burst:           burstSize,
		cleanupInterval: 10 * time.Minute, // Clean up inactive clients after 10 minutes
	}

	// Start cleanup goroutine
	go rl.cleanupRoutine()

	return rl
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
				tokens:   rl.burst,
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

	// Add tokens based on time elapsed
	elapsed := now.Sub(bucket.lastSeen)
	tokensToAdd := int(elapsed.Minutes() * float64(rl.rate))

	bucket.tokens += tokensToAdd
	if bucket.tokens > rl.burst {
		bucket.tokens = rl.burst
	}

	bucket.lastSeen = now

	// Check if we have tokens available
	if bucket.tokens > 0 {
		bucket.tokens--
		return true
	}

	return false
}

// cleanupRoutine removes inactive clients periodically
func (rl *RateLimiter) cleanupRoutine() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		rl.cleanupClients()
	}
}

// cleanupClients removes clients that haven't been seen for a while
func (rl *RateLimiter) cleanupClients() {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	cutoff := time.Now().Add(-rl.cleanupInterval)
	for ip, bucket := range rl.clients {
		bucket.mutex.Lock()
		if bucket.lastSeen.Before(cutoff) {
			delete(rl.clients, ip)
		}
		bucket.mutex.Unlock()
	}
}

// GetClientIP extracts the real client IP from the request
func GetClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (proxy/load balancer)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can contain multiple IPs separated by commas
		// Format: "client, proxy1, proxy2" - we want the first (client) IP
		ips := strings.Split(xff, ",")
		for _, ipStr := range ips {
			cleanIP := strings.TrimSpace(ipStr)
			if cleanIP != "" {
				// Validate IP format and exclude private/reserved ranges for security
				if ip := net.ParseIP(cleanIP); ip != nil && !isPrivateIP(ip) {
					return ip.String()
				}
			}
		}

		// If no public IP found, use the first valid IP (even if private)
		for _, ipStr := range ips {
			cleanIP := strings.TrimSpace(ipStr)
			if ip := net.ParseIP(cleanIP); ip != nil {
				return ip.String()
			}
		}
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		cleanIP := strings.TrimSpace(xri)
		if ip := net.ParseIP(cleanIP); ip != nil {
			return ip.String()
		}
	}

	// Fall back to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := GetClientIP(r)

			if !limiter.Allow(clientIP) {
				logger.SafeWarn("ratelimit", "Rate limit exceeded", map[string]interface{}{
					"client_ip": clientIP,
					"endpoint":  r.URL.Path,
					"method":    r.Method,
				})

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "60") // Suggest retry after 1 minute
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"Rate limit exceeded","retry_after":"60s"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
