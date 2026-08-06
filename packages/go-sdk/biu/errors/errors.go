// Package errors provides structured error types mapped to HTTP / Connect codes.
package errors

import (
	"errors"
	"fmt"
)

type Code string

const (
	CodeUnknown            Code = "UNKNOWN"
	CodeInvalidArgument    Code = "INVALID_ARGUMENT"
	CodeNotFound           Code = "NOT_FOUND"
	CodeAlreadyExists      Code = "ALREADY_EXISTS"
	CodePermissionDenied   Code = "PERMISSION_DENIED"
	CodeUnauthenticated    Code = "UNAUTHENTICATED"
	CodeQuotaExceeded      Code = "QUOTA_EXCEEDED"
	CodeFailedPrecondition Code = "FAILED_PRECONDITION"
	CodeAborted            Code = "ABORTED"
	CodeOutOfRange         Code = "OUT_OF_RANGE"
	CodeUnimplemented      Code = "UNIMPLEMENTED"
	CodeInternal           Code = "INTERNAL"
	CodeUnavailable        Code = "UNAVAILABLE"
	CodeDeadlineExceeded   Code = "DEADLINE_EXCEEDED"
)

// E is the canonical error type. Implements `error` and supports wrapping.
type E struct {
	Code    Code
	Message string
	Details map[string]string
	cause   error
}

func (e *E) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %s", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *E) Unwrap() error { return e.cause }

// New returns a new *E.
func New(code Code, format string, args ...any) *E {
	return &E{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap returns *E that wraps cause.
func Wrap(cause error, code Code, format string, args ...any) *E {
	return &E{Code: code, Message: fmt.Sprintf(format, args...), cause: cause}
}

// WithDetail adds a key=value detail (returns same *E for chaining).
func (e *E) WithDetail(key, value string) *E {
	if e.Details == nil {
		e.Details = make(map[string]string)
	}
	e.Details[key] = value
	return e
}

// IsCode checks if err (or any wrapped err) has the given Code.
func IsCode(err error, code Code) bool {
	var e *E
	if errors.As(err, &e) {
		return e.Code == code
	}
	return false
}

// Convenient constructors
func InvalidArgument(format string, args ...any) *E { return New(CodeInvalidArgument, format, args...) }
func NotFound(format string, args ...any) *E        { return New(CodeNotFound, format, args...) }
func PermissionDenied(format string, args ...any) *E {
	return New(CodePermissionDenied, format, args...)
}
func Unauthenticated(format string, args ...any) *E { return New(CodeUnauthenticated, format, args...) }
func QuotaExceeded(format string, args ...any) *E   { return New(CodeQuotaExceeded, format, args...) }
func Internal(format string, args ...any) *E        { return New(CodeInternal, format, args...) }
