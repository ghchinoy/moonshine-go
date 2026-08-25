package moonshine

import (
	"errors"
	"fmt"
)

// Standard Moonshine C API error and status codes.
const (
	ErrorCodeNone            int32 = 0
	ErrorCodeUnknown         int32 = -1
	ErrorCodeInvalidHandle   int32 = -2
	ErrorCodeInvalidArgument int32 = -3
	ErrorCodeBusy            int32 = -4

	TTSStatusNeedText    int32 = 1
	TTSStatusEndOfStream int32 = 2
	TTSStatusCancelled   int32 = 3
)

var (
	// ErrBusy indicates a streaming generation is already in flight on the synthesizer.
	ErrBusy = errors.New("moonshine: synthesizer is busy with a streaming generation")

	// ErrNeedText indicates that more text is needed to form a complete utterance chunk.
	ErrNeedText = errors.New("moonshine: more text is needed to produce a chunk")

	// ErrEndOfStream indicates that input ended and all queued chunks have been synthesized.
	ErrEndOfStream = errors.New("moonshine: end of audio stream reached")

	// ErrCancelled indicates that streaming generation was cancelled via barge-in.
	ErrCancelled = errors.New("moonshine: streaming generation was cancelled")
)

// Error wraps a moonshine_error_t-style negative/non-zero return code with
// the library's own human-readable description (moonshine_error_to_string).
type Error struct {
	Code int32
	Op   string
}

func (e *Error) Error() string {
	msg := errorToString(e.Code)
	if msg == "" {
		msg = "unknown error"
	}
	return fmt.Sprintf("moonshine: %s: %s (code %d)", e.Op, msg, e.Code)
}

// checkCode converts a moonshine C API return code into a Go error, or nil
// on success (MOONSHINE_ERROR_NONE == 0).
func checkCode(op string, code int32) error {
	if code == 0 {
		return nil
	}
	return &Error{Code: code, Op: op}
}

// checkHandle converts a moonshine C API handle-returning call into
// (handle, error): negative handles are errors, per moonshine-c-api.h.
func checkHandle(op string, handle int32) (int32, error) {
	if handle < 0 {
		return 0, &Error{Code: handle, Op: op}
	}
	return handle, nil
}
