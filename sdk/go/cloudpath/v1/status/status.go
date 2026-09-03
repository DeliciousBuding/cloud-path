// Package status defines the handwritten RPC status type shared by the
// driver and application protocol packages.
//
// The code table mirrors the Status enum in proto/cloudpath/v1/*.proto.
// No generated code and no third-party dependencies are used anywhere in
// sdk/go.
package status

import "fmt"

// Code identifies an RPC/stream terminal state. Values are stable across
// transports and must not be renumbered.
type Code uint32

const (
	CodeOK Code = 0
	// CodeCanceled means the operation was canceled, usually by the caller.
	CodeCanceled Code = 1
	// CodeUnknown is used when the error does not fit another code.
	CodeUnknown Code = 2
	// CodeInvalidArgument indicates malformed or semantically invalid input.
	CodeInvalidArgument Code = 3
	// CodeDeadlineExceeded means the operation finished after its deadline.
	CodeDeadlineExceeded Code = 4
	// CodeNotFound means the referenced entity does not exist.
	CodeNotFound Code = 5
	// CodeAlreadyExists means the idempotency/conflict key already exists.
	CodeAlreadyExists Code = 6
	// CodePermissionDenied means the plugin exceeded its granted permissions.
	CodePermissionDenied Code = 7
	// CodeResourceExhausted indicates rate/size/queue limits were hit.
	CodeResourceExhausted Code = 8
	// CodeFailedPrecondition indicates the system is not in a required state.
	CodeFailedPrecondition Code = 9
	// CodeAborted indicates a concurrency/retry conflict.
	CodeAborted Code = 10
	// CodeOutOfRange indicates a numeric range violation.
	CodeOutOfRange Code = 11
	// CodeUnimplemented means the method is not implemented by this plugin.
	CodeUnimplemented Code = 12
	// CodeInternal is a plugin-internal failure.
	CodeInternal Code = 13
	// CodeUnavailable means the transport/plugin is currently unreachable.
	CodeUnavailable Code = 14
	// CodeDataLoss indicates unrecoverable data loss.
	CodeDataLoss Code = 15
	// CodeUnauthenticated indicates a missing/invalid launch identity.
	CodeUnauthenticated Code = 16
)

// CodeString returns the stable wire name for c, or "UNKNOWN".
func CodeString(c Code) string {
	switch c {
	case CodeOK:
		return "OK"
	case CodeCanceled:
		return "CANCELLED"
	case CodeUnknown:
		return "UNKNOWN"
	case CodeInvalidArgument:
		return "INVALID_ARGUMENT"
	case CodeDeadlineExceeded:
		return "DEADLINE_EXCEEDED"
	case CodeNotFound:
		return "NOT_FOUND"
	case CodeAlreadyExists:
		return "ALREADY_EXISTS"
	case CodePermissionDenied:
		return "PERMISSION_DENIED"
	case CodeResourceExhausted:
		return "RESOURCE_EXHAUSTED"
	case CodeFailedPrecondition:
		return "FAILED_PRECONDITION"
	case CodeAborted:
		return "ABORTED"
	case CodeOutOfRange:
		return "OUT_OF_RANGE"
	case CodeUnimplemented:
		return "UNIMPLEMENTED"
	case CodeInternal:
		return "INTERNAL"
	case CodeUnavailable:
		return "UNAVAILABLE"
	case CodeDataLoss:
		return "DATA_LOSS"
	case CodeUnauthenticated:
		return "UNAUTHENTICATED"
	default:
		return "UNKNOWN"
	}
}

// Status is the wire and error representation of a terminal state.
type Status struct {
	Code    Code   `json:"code"`
	Message string `json:"message,omitempty"`
}

// New returns an OK status.
func New() *Status { return &Status{Code: CodeOK} }

// Errorf builds an error status with a formatted message.
func Errorf(code Code, format string, args ...any) *Status {
	return &Status{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Error implements error.
func (s *Status) Error() string {
	if s == nil {
		return "rpc status: nil"
	}
	return "rpc status " + CodeString(s.Code) + ": " + s.Message
}

// IsOK reports whether s is nil or carries CodeOK.
func (s *Status) IsOK() bool { return s == nil || s.Code == CodeOK }
