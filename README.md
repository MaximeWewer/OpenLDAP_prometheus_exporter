# OpenLDAP Prometheus Exporter

A Prometheus exporter for OpenLDAP using the [*Monitor backend*](https://www.openldap.org/doc/admin26/monitoringslapd.html)

## Support

- Compatible with OpenLDAP 2.4+
- Tested with Bitnami OpenLDAP container

## Required environment variables

| Variable | Description | Example |
|----------|-------------|---------|
| `LDAP_URL` | LDAP server URL | `ldap://openldap:1389` or `ldaps://openldap:1636` |
| `LDAP_USERNAME` | Connection DN (**prefer adminconfig**) | `cn=adminconfig,cn=config` |
| `LDAP_PASSWORD` | Account password | `adminpasswordconfig` |

> **Recommendation:** Use the `adminconfig` account which has access to `cn=config` and the Monitor backend.

## Optional environment variables

| Variable | Description | Default |
|----------|-------------|---------|
| `LDAP_SERVER_NAME` | Server name in metrics | `openldap` |
| `LOG_LEVEL` | Log level (DEBUG, INFO, WARN, ERROR, FATAL) | `INFO` |
| `LDAP_TLS` | Enable TLS | `false` |
| `LDAP_TLS_SKIP_VERIFY` | Skip certificate verification | `false` |
| `LDAP_TLS_CA` | Path to CA certificate | |
| `LDAP_TLS_CERT` | Path to client certificate | |
| `LDAP_TLS_KEY` | Path to client private key | |
| `LDAP_TIMEOUT` | Connection timeout (seconds) | `10` |
| `LDAP_UPDATE_EVERY` | Collection interval (seconds) | `15` |

## Required OpenLDAP configuration

### 1. Monitor backend activation

The Monitor backend must be enabled on your OpenLDAP server.

### 2. Base DN monitoring activation

⚠️ **Important:** You must manually activate monitoring for each **Base DN** you want to monitor. The exporter automatically detects configured bases but cannot activate them.

### 3. ACL configuration

The account used (recommended: `adminconfig`) must have read access to the Monitor backend:

```ldif
# Example ACL for adminconfig
dn: olcDatabase={1}monitor,cn=config
changetype: modify
replace: olcAccess
olcAccess: to dn.subtree="cn=Monitor" by dn.exact="cn=adminconfig,cn=config" read by * none
```

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

### System informations

- `openldap_backends_info{server,backend,type}` - Information about available backends
- `openldap_overlays_info{server,overlay,status}` - Information about loaded overlays
- `openldap_listeners_info{server,listener,address}` - Information about active listeners
- `openldap_tls_info{server,component,status}` - TLS configuration information

### Databases

- `openldap_database_entries{server,base_dn}` - Entries per database/DN

### Time and Health

- `openldap_server_time{server}` - Current server timestamp
- `openldap_server_uptime_seconds{server}` - Server uptime in seconds
- `openldap_health_status{server}` - Server health status (1=healthy, 0=unhealthy)
- `openldap_response_time_seconds{server}` - Health check response time
- `openldap_scrape_errors_total{server}` - Total collection errors

## Usage

### Docker Compose (recommended)

```yaml
openldap-exporter:
  image: ghcr.io/MaximeWewer/OpenLDAP_prometheus_exporter:latest
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
  ghcr.io/MaximeWewer/OpenLDAP_prometheus_exporter:latest
```

## Endpoints

- `http://localhost:9330/` - Information page with version
- `http://localhost:9330/metrics` - Prometheus metrics
- `http://localhost:9330/health` - JSON health check

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

## Technical architecture

- **Language:** Go 1.24
- **Logging:** [rs/zerolog](https://github.com/rs/zerolog) for high-performance structured logs
- **LDAP:** [go-ldap/ldap/v3](github.com/go-ldap/ldap)
- **Metrics:** Official Prometheus client
- **Container:** Distroless image for security
- **Build:** Multi-stage Docker build with static binary

## Development

### Code Structure

```text
├── main.go           # Entry point and HTTP configuration
├── config.go         # Configuration management
├── logger.go         # Centralized logging system
├── ldap_client.go    # LDAP client with TLS management
├── exporter.go       # Prometheus metrics collector
├── metrics.go        # Metrics definitions
└── Dockerfile        # Optimized multi-stage build
```

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
