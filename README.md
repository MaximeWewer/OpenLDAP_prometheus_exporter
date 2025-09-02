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
services:
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
    logging:
      driver: json-file
      options:
        "max-size": "10m"
        "max-file": "5"
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

**Important:** You must manually activate monitoring for each **Base DN** you want to monitor. The exporter automatically detects configured bases but cannot activate them.

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
| `RATE_LIMIT_ENABLED` | Enable/disable rate limiting | `true` | `false` |
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
| `OPENLDAP_METRICS_INCLUDE` | Only collect these metric groups | `connections,statistics,health,server` |
| `OPENLDAP_METRICS_EXCLUDE` | Exclude these metric groups | `overlays,tls,backends,log,sasl` |

**Available metric groups:** `connections`, `statistics`, `operations`, `threads`, `time`, `waiters`, `overlays`, `tls`, `backends`, `listeners`, `health`, `database`, `server`, `log`, `sasl`

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

The exporter exposes two types of metrics:

1. **OpenLDAP metrics** (namespace `openldap`) - collected from LDAP server via `cn=Monitor`
2. **Internal metrics** (namespace `openldap_exporter`) - exporter performance and health

### OpenLDAP metrics (namespace: `openldap`)

These metrics are collected in different configurable groups. Use `OPENLDAP_METRICS_INCLUDE` or `OPENLDAP_METRICS_EXCLUDE` to filter groups according to your needs.

### Connections (`connections`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_connections_current` | Gauge | `server` | Current number of connections | `cn=Current,cn=Connections,cn=Monitor` |
| `openldap_connections_total` | Counter | `server` | Total number of connections (cumulative) | `cn=Total,cn=Connections,cn=Monitor` |

### Statistics (`statistics`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_bytes_total` | Counter | `server` | Total bytes transmitted | `cn=Bytes,cn=Statistics,cn=Monitor` |
| `openldap_entries_total` | Counter | `server` | Total entries sent | `cn=Entries,cn=Statistics,cn=Monitor` |
| `openldap_referrals_total` | Counter | `server` | Total referrals sent | `cn=Referrals,cn=Statistics,cn=Monitor` |
| `openldap_pdu_total` | Counter | `server` | Total PDUs processed | `cn=PDU,cn=Statistics,cn=Monitor` |

### Operations (`operations`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_operations_initiated_total` | Counter | `server`, `operation` | Operations initiated by type | `cn=Operations,cn=Monitor` |
| `openldap_operations_completed_total` | Counter | `server`, `operation` | Operations completed by type | `cn=Operations,cn=Monitor` |

**Available operation types:** `bind`, `unbind`, `add`, `delete`, `modify`, `modrdn`, `compare`, `search`, `abandon`, `extended`

### Threads (`threads`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_threads_max` | Gauge | `server` | Maximum number of threads | `cn=Max,cn=Threads,cn=Monitor` |
| `openldap_threads_max_pending` | Gauge | `server` | Maximum number of pending threads | `cn=Max Pending,cn=Threads,cn=Monitor` |
| `openldap_threads_backload` | Gauge | `server` | Thread workload | `cn=Backload,cn=Threads,cn=Monitor` |
| `openldap_threads_active` | Gauge | `server` | Active threads | `cn=Active,cn=Threads,cn=Monitor` |
| `openldap_threads_open` | Gauge | `server` | Open threads | `cn=Open,cn=Threads,cn=Monitor` |
| `openldap_threads_starting` | Gauge | `server` | Starting threads | `cn=Starting,cn=Threads,cn=Monitor` |
| `openldap_threads_pending` | Gauge | `server` | Pending threads | `cn=Pending,cn=Threads,cn=Monitor` |
| `openldap_threads_state` | Gauge | `server`, `state` | Thread pool state | `cn=State,cn=Threads,cn=Monitor` |

### Time (`time`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_server_time` | Gauge | `server` | Current server timestamp | `cn=Current,cn=Time,cn=Monitor` |
| `openldap_server_uptime_seconds` | Gauge | `server` | Server uptime in seconds | `cn=Start,cn=Time,cn=Monitor` |

### Waiters (`waiters`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_waiters_read` | Gauge | `server` | Number of read waiters | `cn=Read,cn=Waiters,cn=Monitor` |
| `openldap_waiters_write` | Gauge | `server` | Number of write waiters | `cn=Write,cn=Waiters,cn=Monitor` |

### Overlays (`overlays`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_overlays_info` | Gauge | `server`, `overlay`, `status` | Information about loaded overlays | `cn=Overlays,cn=Monitor` |

### TLS (`tls`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_tls_info` | Gauge | `server`, `component`, `status` | TLS configuration information | `cn=TLS,cn=Monitor` |

### Backends (`backends`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_backends_info` | Gauge | `server`, `backend`, `type` | Information about available backends | `cn=Backends,cn=Monitor` |

### Listeners (`listeners`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_listeners_info` | Gauge | `server`, `listener`, `address` | Information about active listeners | `cn=Listeners,cn=Monitor` |

### Database (`database`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_database_entries` | Gauge | `server`, `base_dn`, `domain_component` | Number of entries per base DN with domain component filtering | `cn=Database,cn=Monitor` |
| `openldap_database_info` | Gauge | `server`, `base_dn`, `is_shadow`, `context`, `readonly` | Database metadata | `cn=Database,cn=Monitor` |

### Server (`server`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_server_info` | Gauge | `server`, `version`, `description` | OpenLDAP server version and description | `cn=Monitor` |

### Log (`log`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_log_level_enabled` | Gauge | `server`, `log_type` | Enabled log levels (1=enabled, 0=disabled) | `cn=Log,cn=Monitor` |

**Available log types:** `Trace`, `Packets`, `Args`, `Conns`, `BER`, `Filter`, `Config`, `ACL`, `Stats`, `Stats2`, `Shell`, `Parse`, `Sync`

### SASL (`sasl`)

| Metric | Type | Labels | Description | LDAP Source |
|----------|------|---------|-------------|-------------|
| `openldap_sasl_info` | Gauge | `server`, `mechanism`, `status` | SASL authentication mechanisms configuration | `cn=SASL,cn=Monitor` |

### Health (`health`)

| Metric | Type | Labels | Description | Source |
|----------|------|---------|-------------|--------|
| `openldap_health_status` | Gauge | `server` | Server health status (1=healthy, 0=unhealthy) | Calculated |
| `openldap_response_time_seconds` | Gauge | `server` | Health check response time | Calculated |
| `openldap_scrape_errors_total` | Counter | `server` | Total number of scrape errors | Internal |
| `openldap_up` | Gauge | `server` | Whether the OpenLDAP exporter is up (1) or not (0) | Internal |

### Internal exporter metrics (namespace: `openldap_exporter`)

These metrics are exposed on the `/internal/metrics` endpoint and allow monitoring the performance and health of the exporter itself.

#### Connection pool (`openldap_exporter_pool`)

**Basic metrics**

| Metric | Type | Labels | Description |
|----------|------|---------|-------------|
| `openldap_exporter_pool_utilization_ratio` | Gauge | `server`, `pool_type` | Connection pool utilization ratio (0-1) |
| `openldap_exporter_pool_connections` | Gauge | `server`, `pool_type`, `state` | Number of connections by state (active, idle, total) |
| `openldap_exporter_pool_operations_total` | Counter | `server`, `pool_type`, `operation` | Total number of pool operations (get, put, create, close) |

**Connection lifecycle**

| Metric | Type | Labels | Description |
|----------|------|---------|-------------|
| `openldap_exporter_pool_connections_created_total` | Counter | `server`, `pool_type` | Total number of connections created |
| `openldap_exporter_pool_connections_closed_total` | Counter | `server`, `pool_type`, `reason` | Total number of connections closed (reason: normal, timeout, error, shutdown) |
| `openldap_exporter_pool_connections_failed_total` | Counter | `server`, `pool_type`, `error` | Total number of failed connection attempts |
| `openldap_exporter_pool_connections_reused_total` | Counter | `server`, `pool_type` | Total number of connections reused from the pool |

**Connection quality**

| Metric | Type | Labels | Description |
|----------|------|---------|-------------|
| `openldap_exporter_pool_wait_time_seconds` | Histogram | `server`, `pool_type` | Wait time to get a connection from the pool |
| `openldap_exporter_pool_wait_timeouts_total` | Counter | `server`, `pool_type` | Total number of connection wait timeouts |

**Health monitoring**

| Metric | Type | Labels | Description |
|----------|------|---------|-------------|
| `openldap_exporter_pool_health_checks_total` | Counter | `server`, `pool_type` | Total number of health checks performed |
| `openldap_exporter_pool_health_check_failures_total` | Counter | `server`, `pool_type` | Total number of health check failures |

**Operation details**

| Metric | Type | Labels | Description |
|----------|------|---------|-------------|
| `openldap_exporter_pool_get_requests_total` | Counter | `server`, `pool_type` | Total number of connection get requests |
| `openldap_exporter_pool_get_failures_total` | Counter | `server`, `pool_type`, `reason` | Total number of connection get failures (reason: timeout, creation_failed, max_attempts) |
| `openldap_exporter_pool_put_requests_total` | Counter | `server`, `pool_type` | Total number of connection put requests |
| `openldap_exporter_pool_put_rejections_total` | Counter | `server`, `pool_type`, `reason` | Total number of connection put rejections (reason: invalid_connection, pool_full) |

#### Circuit breaker (`openldap_exporter_circuit_breaker`)

| Metric | Type | Labels | Description |
|----------|------|---------|-------------|
| `openldap_exporter_circuit_breaker_state` | Gauge | `server` | Circuit breaker state (0=closed, 1=half-open, 2=open) |
| `openldap_exporter_circuit_breaker_requests_total` | Counter | `server`, `result` | Total number of circuit breaker requests (allowed, blocked) |
| `openldap_exporter_circuit_breaker_failures_total` | Counter | `server` | Total number of circuit breaker failures |

#### Metrics collection (`openldap_exporter_collection`)

| Metric | Type | Labels | Description |
|----------|------|---------|-------------|
| `openldap_exporter_collection_latency_seconds` | Histogram | `server`, `metric_type` | Metrics collection latency |
| `openldap_exporter_collection_success_total` | Counter | `server`, `metric_type` | Total number of successful collections |
| `openldap_exporter_collection_failures_total` | Counter | `server`, `metric_type` | Total number of collection failures |


#### Rate limiting (`openldap_exporter_rate_limit`)

| Metric | Type | Labels | Description |
|----------|------|---------|-------------|
| `openldap_exporter_rate_limit_requests_total` | Counter | `client_ip`, `endpoint` | Total number of requests with rate limiting |
| `openldap_exporter_rate_limit_blocked_total` | Counter | `client_ip`, `endpoint` | Total number of requests blocked by rate limiting |

#### System (`openldap_exporter_system`)

| Metric | Type | Labels | Description |
|----------|------|---------|-------------|
| `openldap_exporter_system_goroutines` | Gauge | `server` | Number of active goroutines |
| `openldap_exporter_system_memory_bytes` | Gauge | `server`, `type` | Memory usage in bytes (heap, stack, sys) |
| `openldap_exporter_system_uptime_seconds` | Gauge | `server` | Exporter uptime in seconds |

## Endpoints

All endpoints include security headers and are subject to configured rate limiting.

### Main endpoints

| Endpoint | Type | Description | Content |
|----------|------|-------------|---------|
| `/` | HTML | Information page with version and configuration status | Web page showing version, active metric filters (include/exclude) |
| `/health` | JSON | Health check with status information | `{"status":"ok","version":"x.x.x","timestamp":"2025-09-02T14:44:30Z","uptime":"12.64s"}` |
| `/metrics` | Text | OpenLDAP Prometheus metrics | All OpenLDAP metrics (namespace `openldap`) according to configured filtering |

### Internal endpoints

| Endpoint | Type | Description | Content |
|----------|------|-------------|---------|
| `/internal/metrics` | Text | Internal exporter metrics | Performance metrics (namespace `openldap_exporter`): pool, circuit breaker, collection, rate limiting, system |

### Rate limiting configuration

- **Endpoints `/metrics`, `/internal/metrics`**: `RATE_LIMIT_REQUESTS` req/min (default: 30), burst `RATE_LIMIT_BURST` (default: 10)
- **Endpoint `/health`**: `HEALTH_RATE_LIMIT_REQUESTS` req/min (default: 60), burst `HEALTH_RATE_LIMIT_BURST` (default: 20)
- **Endpoint `/`**: Same limit as `/metrics`

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
