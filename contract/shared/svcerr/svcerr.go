package svcerr

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SentinelError carries a gRPC code and a message. It implements both error
// and the GRPCStatus interface that grpc-go honors via status.FromError, so
// returning a wrapped sentinel from a handler results in the correct wire
// status without an explicit mapping table.
type SentinelError struct {
	code codes.Code
	msg  string
}

// New constructs a SentinelError. Use as a package-level var:
//
//	var ErrAccountLocked = svcerr.New(codes.PermissionDenied, "account locked")
func New(code codes.Code, msg string) *SentinelError {
	return &SentinelError{code: code, msg: msg}
}

func (e *SentinelError) Error() string { return e.msg }

func (e *SentinelError) GRPCStatus() *status.Status {
	return status.New(e.code, e.msg)
}
