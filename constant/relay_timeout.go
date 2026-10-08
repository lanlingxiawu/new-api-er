package constant

// Per-user relay timeout values stored as text. The user cache, the request
// context and the timeout service all fill in the same defaults for an empty
// stored value; they share these constants so the defaults cannot drift apart.
const (
	// RelayStreamResponseTimeoutModeFirstOutput times a stream's response window
	// until its first output; the default mode.
	RelayStreamResponseTimeoutModeFirstOutput = "first_output"
	// RelayStreamResponseTimeoutModeIdle times the gap between outputs.
	RelayStreamResponseTimeoutModeIdle = "idle"

	// NonStreamTimeoutBillingRefund returns the pre-consumed quota when a
	// non-stream request hits its deadline: the platform absorbs the upstream
	// cost for tokens the user never received. The default.
	NonStreamTimeoutBillingRefund = "refund"
	// NonStreamTimeoutBillingCharge settles the usage actually received instead
	// of refunding. It also makes the request talk to upstream in streaming mode
	// so that partial usage exists to settle at all — a non-stream upstream call
	// yields nothing before its first byte, which is exactly when the response
	// deadline fires.
	NonStreamTimeoutBillingCharge = "charge"
	// NonStreamTimeoutBillingInput settles only the input when our deadline
	// ends a request before anything was delivered: the input the upstream
	// confirmed, else the pre-consume estimate; output is never charged, so the
	// platform bears only the output side. Per-call priced models have no input
	// share and are refunded instead.
	NonStreamTimeoutBillingInput = "input"
)
