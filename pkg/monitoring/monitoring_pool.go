package monitoring

import "time"

// RecordPoolUtilization records connection pool utilization
func (im *InternalMonitoring) RecordPoolUtilization(server, poolType string, utilization float64) {
	im.poolUtilization.WithLabelValues(server, poolType).Set(utilization)
}

// RecordPoolConnections records connection pool connection counts
func (im *InternalMonitoring) RecordPoolConnections(server, poolType, state string, count float64) {
	im.poolConnections.WithLabelValues(server, poolType, state).Set(count)
}

// RecordPoolWaitTime records time spent waiting for a connection
func (im *InternalMonitoring) RecordPoolWaitTime(server, poolType string, duration time.Duration) {
	im.poolWaitTime.WithLabelValues(server, poolType).Observe(duration.Seconds())
}

// RecordPoolOperation records pool operations
func (im *InternalMonitoring) RecordPoolOperation(server, poolType, operation string) {
	im.poolOperations.WithLabelValues(server, poolType, operation).Inc()
}

// RecordPoolConnectionCreated records connection creation
func (im *InternalMonitoring) RecordPoolConnectionCreated(server, poolType string) {
	im.poolConnectionsCreated.WithLabelValues(server, poolType).Inc()
}

// RecordPoolConnectionClosed records connection closure
func (im *InternalMonitoring) RecordPoolConnectionClosed(server, poolType, reason string) {
	im.poolConnectionsClosed.WithLabelValues(server, poolType, reason).Inc()
}

// RecordPoolConnectionFailed records connection failure
func (im *InternalMonitoring) RecordPoolConnectionFailed(server, poolType, errorType string) {
	im.poolConnectionsFailed.WithLabelValues(server, poolType, errorType).Inc()
}

// RecordPoolConnectionReused records connection reuse from pool
func (im *InternalMonitoring) RecordPoolConnectionReused(server, poolType string) {
	im.poolConnectionsReused.WithLabelValues(server, poolType).Inc()
}

// RecordPoolConnectionAge records connection age when closed
func (im *InternalMonitoring) RecordPoolConnectionAge(server, poolType string, age time.Duration) {
	im.poolConnectionAge.WithLabelValues(server, poolType).Observe(age.Seconds())
}

// RecordPoolConnectionIdleTime records connection idle time
func (im *InternalMonitoring) RecordPoolConnectionIdleTime(server, poolType string, idleTime time.Duration) {
	im.poolConnectionIdleTime.WithLabelValues(server, poolType).Observe(idleTime.Seconds())
}

// RecordPoolWaitTimeout records connection wait timeout
func (im *InternalMonitoring) RecordPoolWaitTimeout(server, poolType string) {
	im.poolWaitTimeouts.WithLabelValues(server, poolType).Inc()
}

// RecordPoolHealthCheck records health check execution with result
func (im *InternalMonitoring) RecordPoolHealthCheck(server, poolType, result string) {
	im.poolHealthChecks.WithLabelValues(server, poolType, result).Inc()
}

// RecordPoolHealthCheckFailure records health check failure
func (im *InternalMonitoring) RecordPoolHealthCheckFailure(server, poolType string) {
	im.poolHealthCheckFailures.WithLabelValues(server, poolType).Inc()
}

// RecordPoolGetRequest records connection get request
func (im *InternalMonitoring) RecordPoolGetRequest(server, poolType string) {
	im.poolGetRequests.WithLabelValues(server, poolType).Inc()
}

// RecordPoolGetFailure records connection get failure
func (im *InternalMonitoring) RecordPoolGetFailure(server, poolType, reason string) {
	im.poolGetFailures.WithLabelValues(server, poolType, reason).Inc()
}

// RecordPoolPutRequest records connection put (return) request
func (im *InternalMonitoring) RecordPoolPutRequest(server, poolType string) {
	im.poolPutRequests.WithLabelValues(server, poolType).Inc()
}

// RecordPoolPutRejection records connection put rejection
func (im *InternalMonitoring) RecordPoolPutRejection(server, poolType, reason string) {
	im.poolPutRejections.WithLabelValues(server, poolType, reason).Inc()
}
