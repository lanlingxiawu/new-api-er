package perfmetrics

// retryGuardLatency distinguishes first-response delay from stream duration.
// A successful stream with no reliable first-response marker is not slow data.
func retryGuardLatency(stream, hasFirst, success bool, first, total int64) int64 {
	if hasFirst {
		return first
	}
	if stream && success {
		return -1
	}
	return total
}
