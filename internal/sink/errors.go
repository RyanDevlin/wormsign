package sink

import "errors"

// Sentinel errors used across sink implementations.
var (
	errNilReport = errors.New("report must not be nil")
	errNilLogger = errors.New("logger must not be nil")
)
