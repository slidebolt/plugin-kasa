package kasa

import "errors"

// Error constants for standardized error reporting
var (
	// ErrOffline indicates the device is offline or unreachable
	ErrOffline = errors.New("device is offline")

	// ErrTimeout indicates a network timeout occurred
	ErrTimeout = errors.New("connection timeout")

	// ErrUnauthorized indicates authentication failure
	ErrUnauthorized = errors.New("authentication failed")

	// ErrInvalidResponse indicates the device returned an invalid or malformed response
	ErrInvalidResponse = errors.New("invalid device response")

	// ErrNetwork indicates a general network error
	ErrNetwork = errors.New("network error")

	// ErrUnknown indicates an unknown error occurred
	ErrUnknown = errors.New("unknown error")
)

// ErrorWithState wraps an error with device state information
type ErrorWithState struct {
	Err       error
	Power     bool
	ErrorCode string
}

func (e *ErrorWithState) Error() string {
	return e.Err.Error()
}

func (e *ErrorWithState) Unwrap() error {
	return e.Err
}

// IsOfflineError returns true if the error indicates the device is offline
func IsOfflineError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrOffline) || errors.Is(err, ErrTimeout)
}
