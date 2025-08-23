# OpenLDAP Prometheus Exporter

A Prometheus exporter for OpenLDAP with advanced security features, performance optimizations, and comprehensive monitoring using the [*Monitor backend*](https://www.openldap.org/doc/admin26/monitoringslapd.html)

## Features

### Security features

- **Password Protection**: Credentials are encrypted in memory using ChaCha20-Poly1305
- **Input Validation**: Comprehensive LDAP input validation to prevent injection attacks
- **Rate Limiting**: Configurable rate limiting on all endpoints
- **Security Headers**: Full set of security headers (CSP, HSTS, X-Frame-Options, etc.)
- **Circuit Breaker**: Protection against cascading failures
- **Safe Logging**: Automatic redaction of sensitive information in logs

### Performance optimizations

- **Connection Pooling**: Reusable LDAP connections with configurable pool size
- **Atomic Operations**: Lock-free metrics updates for better concurrency
- **Metric Filtering**: Reduce overhead by collecting only needed metrics
- **Domain Filtering**: Filter by domain components to reduce LDAP queries
- **Retry Logic**: Exponential backoff with jitter for transient failures

## Support

- Compatible with OpenLDAP 2.4+
- Tested with Bitnami OpenLDAP container

## Quick Start

### Docker compose (recommended)

```yaml
openldap-exporter:
  image: ghcr.io/maximewewer/openldap_prometheus_exporter:latest
  hostname: openldap-exporter
  container_name: openldap-exporter
  restart: unless-stopped
  ports:
    - "9330:9330"
  environment:
    # LDAP Configuration
    - LDAP_URL=ldap://openldap:1389
    - LDAP_USERNAME=cn=adminconfig,cn=config
    - LDAP_PASSWORD=adminpasswordconfig
    - LDAP_SERVER_NAME=prod-ldap-01
    
    # Logging Configuration
    - LOG_LEVEL=INFO  # DEBUG for more details
    
    # TLS Configuration (if needed)
    - LDAP_TLS=false
    - LDAP_TLS_SKIP_VERIFY=false
    - LDAP_TIMEOUT=10
    - LDAP_UPDATE_EVERY=15
```

### Docker

```bash
docker run -p 9330:9330 \
  -e LDAP_URL=ldap://myldap.domain.com:389 \
  -e LDAP_USERNAME=cn=adminconfig,cn=config \
  -e LDAP_PASSWORD=secret \
  -e LDAP_SERVER_NAME=ldap-prod-01 \
  -e LOG_LEVEL=INFO \
  ghcr.io/maximewewer/openldap_prometheus_exporter:latest
```

## Prometheus configuration

```yaml
scrape_configs:
  - job_name: 'openldap'
    static_configs:
      - targets: ['openldap-exporter:9330']
    scrape_interval: 15s
    scrape_timeout: 10s
    metrics_path: /metrics
```

## Required OpenLDAP configuration

### 1. Monitor backend activation

The Monitor backend must be enabled on your OpenLDAP server.

### 2. Base DN monitoring activation

⚠️ **Important:** You must manually activate monitoring for each **Base DN** you want to monitor. The exporter automatically detects configured bases but cannot activate them.

### 3. ACL configuration

The account used (recommended: `adminconfig`. `LDAP_CONFIG_ADMIN` is used) must have read access to the Monitor backend:

```ldif
# Example ACL for adminconfig
dn: olcDatabase={1}monitor,cn=config
changetype: modify
replace: olcAccess
olcAccess: to dn.subtree="cn=Monitor" by dn.exact="cn=adminconfig,cn=config" read by * none
```

## Configuration

### LDAP configuration (required)

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `LDAP_URL` | LDAP server URL | *Required* | `ldap://localhost:389` |
| `LDAP_USERNAME` | Username for LDAP authentication | *Required* | `cn=admin,dc=example,dc=com` |
| `LDAP_PASSWORD` | Password for LDAP authentication | *Required* | `password123` |

> **Recommendation:** Use the `adminconfig` account which has access to `cn=config` and the Monitor backend.

### LDAP configuration (optional)

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `LDAP_SERVER_NAME` | LDAP server name for logs and metrics | `openldap` | `ldap-prod-01` |
| `LDAP_TLS` | Use TLS for connection | `false` | `true` |
| `LDAP_TIMEOUT` | LDAP connection timeout (seconds) | `10` | `30` |
| `LDAP_UPDATE_EVERY` | Metrics update interval (seconds) | `15` | `30` |
| `LDAP_TLS_SKIP_VERIFY` | Skip TLS certificate verification | `false` | `true` |
| `LDAP_TLS_CA` | Path to CA certificate for TLS | | `/path/to/ca.crt` |
| `LDAP_TLS_CERT` | Path to client certificate | | `/path/to/client.crt` |
| `LDAP_TLS_KEY` | Path to client private key | | `/path/to/client.key` |

### HTTP server configuration

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `LISTEN_ADDRESS` | HTTP server listen address | `:9330` | `:8080` |
| `HTTP_READ_TIMEOUT` | HTTP read timeout | `10s` | `30s` |
| `HTTP_WRITE_TIMEOUT` | HTTP write timeout | `10s` | `30s` |
| `HTTP_IDLE_TIMEOUT` | HTTP idle timeout | `60s` | `120s` |
| `HTTP_SHUTDOWN_TIMEOUT` | Graceful shutdown timeout | `30s` | `60s` |

### Rate limiting configuration

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `RATE_LIMIT_REQUESTS` | Requests per minute for /metrics | `30` | `100` |
| `RATE_LIMIT_BURST` | Burst size for /metrics | `10` | `20` |
| `HEALTH_RATE_LIMIT_REQUESTS` | Requests per minute for /health | `60` | `120` |
| `HEALTH_RATE_LIMIT_BURST` | Burst size for /health | `20` | `40` |

### Logging configuration

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `LOG_LEVEL` | Log level | `INFO` | `DEBUG` |

**Available log levels:**

- `DEBUG`: All logs (very verbose)
- `INFO`: General information
- `WARN`: Warnings
- `ERROR`: Errors only
- `FATAL`: Fatal errors only

### Metrics filtering configuration

| Variable | Description | Example |
|----------|-------------|---------|
| `OPENLDAP_METRICS_INCLUDE` | Only collect these metric groups | `connections,statistics,health` |
| `OPENLDAP_METRICS_EXCLUDE` | Exclude these metric groups | `overlays,tls,backends` |

**Available metric groups:** `connections`, `statistics`, `operations`, `threads`, `time`, `waiters`, `overlays`, `tls`, `backends`, `listeners`, `health`, `database`

**Filtering Logic:**

1. **INCLUDE Mode**: If `OPENLDAP_METRICS_INCLUDE` is defined, only listed metrics are collected
2. **EXCLUDE Mode**: If `OPENLDAP_METRICS_EXCLUDE` is defined (without INCLUDE), all metrics except listed ones are collected
3. **DEFAULT Mode**: If no filters are defined, all metrics are collected

### Domain component (DC) filtering

| Variable | Description | Example |
|----------|-------------|---------|
| `OPENLDAP_DC_INCLUDE` | Only monitor these domain components | `example,company,test` |
| `OPENLDAP_DC_EXCLUDE` | Exclude these domain components | `dev,staging` |

**Filtering Logic:**

1. **INCLUDE Mode**: If `OPENLDAP_DC_INCLUDE` is defined, only listed domains are monitored
2. **EXCLUDE Mode**: If `OPENLDAP_DC_EXCLUDE` is defined (without INCLUDE), all domains except listed ones are monitored
3. **DEFAULT Mode**: If no filters are defined, all domains are monitored

**Domain Component Extraction:**

- `dc=example,dc=org` → Components: `["example", "org"]`
- `ou=users,dc=company,dc=net` → Components: `["company", "net"]`
- `uid=user1,ou=people,dc=test,dc=local` → Components: `["test", "local"]`

## Exported metrics

### Connections

- `openldap_connections_current{server}` - Current connections
- `openldap_connections_total{server}` - Total connections (cumulative)

### LDAP operations

- `openldap_operations_initiated_total{server,operation}` - Operations initiated by type
- `openldap_operations_completed_total{server,operation}` - Operations completed by type

Operation types: `bind`, `unbind`, `add`, `delete`, `modify`, `modrdn`, `compare`, `search`, `abandon`, `extended`

### Traffic statistics

- `openldap_bytes_total{server}` - Total bytes transmitted
- `openldap_entries_total{server}` - Total entries sent
- `openldap_referrals_total{server}` - Total referrals sent
- `openldap_pdu_total{server}` - Total PDUs processed

### Thread metrics

- `openldap_threads_max{server}` - Maximum number of threads
- `openldap_threads_active{server}` - Active threads
- `openldap_threads_open{server}` - Open threads
- `openldap_threads_pending{server}` - Pending threads
- `openldap_threads_max_pending{server}` - Maximum pending threads
- `openldap_threads_starting{server}` - Starting threads
- `openldap_threads_backload{server}` - Thread backload
- `openldap_threads_state{server,state}` - Thread pool state

### Waiters

- `openldap_waiters_read{server}` - Read waiters
- `openldap_waiters_write{server}` - Write waiters

### System information

- `openldap_backends_info{server,backend,type}` - Information about available backends
- `openldap_overlays_info{server,overlay,status}` - Information about loaded overlays
- `openldap_listeners_info{server,listener,address}` - Information about active listeners
- `openldap_tls_info{server,component,status}` - TLS configuration information

### Databases

- `openldap_database_entries{server,base_dn}` - Entries per database/DN

### Time and health

- `openldap_server_time{server}` - Current server timestamp
- `openldap_server_uptime_seconds{server}` - Server uptime in seconds
- `openldap_health_status{server}` - Server health status (1=healthy, 0=unhealthy)
- `openldap_response_time_seconds{server}` - Health check response time
- `openldap_scrape_errors_total{server}` - Total collection errors

## Endpoints

- `http://localhost:9330/` - Information page with version and configuration status
- `http://localhost:9330/metrics` - Prometheus metrics
- `http://localhost:9330/health` - JSON health check
- `http://localhost:9330/internal/metrics` - Internal exporter metrics (goroutines, memory usage, etc.)
- `http://localhost:9330/internal/status` - JSON status endpoint for monitoring

## Logging

The exporter uses a structured logging system in JSON format:

```json
{"level":"info","component":"openldap-exporter","server":"openldap-dev","duration":"15.711396ms","time":"2025-08-01T09:06:06Z","message":"Metrics collection completed successfully"}
```

**Available log levels:**

- `DEBUG`: Detailed logs of LDAP connections and searches
- `INFO`: Informational logs (default)
- `WARN`: Warnings
- `ERROR`: Non-fatal errors
- `FATAL`: Fatal errors (program termination)

## Troubleshooting

### Common errors

1. **LDAP connection error:**

   ```json
   {"level":"error","component":"openldap-exporter","error":"LDAP Result Code 49 \"Invalid Credentials\""}
   ```

   Check `LDAP_USERNAME` and `LDAP_PASSWORD`

2. **No access to monitor backend:**

   ```json
   {"level":"error","component":"openldap-exporter","error":"LDAP Result Code 32 \"No Such Object\""}
   ```

   Verify that the Monitor backend is enabled and accessible

3. **Invalid TLS certificates:**

   ```json
   {"level":"error","component":"openldap-exporter","error":"x509: certificate signed by unknown authority"}
   ```

   Configure `LDAP_TLS_CA` or use `LDAP_TLS_SKIP_VERIFY=true` (not recommended in production)

## Technical Architecture

- **Language:** Go
- **Logging:** [rs/zerolog](https://github.com/rs/zerolog) for high-performance structured logs
- **LDAP:** [go-ldap/ldap/v3](https://github.com/go-ldap/ldap)
- **Metrics:** Official Prometheus client
- **Security:** ChaCha20-Poly1305 for password encryption, comprehensive input validation
- **Performance:** Connection pooling, circuit breaker pattern, retry with exponential backoff
- **Container:** Distroless image for security
- **Build:** Multi-stage Docker build with static binary

## Development

For development information, see [DEVELOPMENT.md](DEVELOPMENT.md) and [TESTING.md](TESTING.md).
