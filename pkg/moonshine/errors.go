package moonshine

import "fmt"

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
