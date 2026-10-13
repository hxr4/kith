package protocol

import (
	"errors"
	"fmt"
)

// Stable error codes (wire contract, §7.3).
const (
	CodeAuthFailed      = "AUTH_FAILED"
	CodeProtocolError   = "PROTOCOL_ERROR"
	CodeVersionMismatch = "VERSION_MISMATCH"
	CodeNoMatch         = "NO_MATCH"
	CodeBusy            = "BUSY"
	CodePolicyDenied    = "POLICY_DENIED"
	CodeDiskFull        = "DISK_FULL"
	CodeTransferError   = "TRANSFER_ERROR"
	CodeNotFound        = "NOT_FOUND"
	CodePeerUnreachable = "PEER_UNREACHABLE"
	CodeRateLimited     = "RATE_LIMITED"
	CodeCancelledByPeer = "CANCELLED_BY_PEER"
	CodeTimeout         = "TIMEOUT"
	CodeInternal        = "INTERNAL"
)

// Error is the single error shape used on the wire, over IPC and in MCP.
type Error struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
	QueueLen    *int   `json:"queue_len,omitempty"`
}

func (e *Error) Error() string {
	if e.Remediation != "" {
		return fmt.Sprintf("%s: %s\nFix: %s", e.Code, e.Message, e.Remediation)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Errf builds an *Error.
func Errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithFix returns a copy of e with remediation set.
func (e *Error) WithFix(format string, args ...any) *Error {
	c := *e
	c.Remediation = fmt.Sprintf(format, args...)
	return &c
}

// AsError converts any error into an *Error, defaulting to INTERNAL.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}

// CodeOf returns the stable code of err ("" for nil).
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	return AsError(err).Code
}
