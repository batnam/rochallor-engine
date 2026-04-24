// Package api contains shared HTTP and gRPC helpers used by every handler in
// the Engine's REST and gRPC surfaces.
package api

import (
	"encoding/json"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errorBody is the canonical JSON error envelope (per R-011).
//
//	{
//	  "error":  "unsupported-format",
//	  "reason": "<one-line description>"
//	}
type errorBody struct {
	Error  string `json:"error"`
	Reason string `json:"reason"`
}

const (
	// errCodeUnsupportedFormat is the machine-readable error code for rejected content types.
	errCodeUnsupportedFormat = "unsupported-format"
)

// WriteUnsupportedFormat writes HTTP 415 when the client submits an unsupported content type.
func WriteUnsupportedFormat(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusUnsupportedMediaType, errorBody{
		Error:  errCodeUnsupportedFormat,
		Reason: reason,
	})
}

// WriteGone writes HTTP 410 for paths that have been removed from the API.
func WriteGone(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusGone, errorBody{
		Error:  errCodeUnsupportedFormat,
		Reason: reason,
	})
}

// WriteNotFound writes HTTP 404 with a JSON body.
func WriteNotFound(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusNotFound, errorBody{
		Error:  "not-found",
		Reason: reason,
	})
}

// WriteBadRequest writes HTTP 400 with a JSON body.
func WriteBadRequest(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusBadRequest, errorBody{
		Error:  "bad-request",
		Reason: reason,
	})
}

// WriteInternalError writes HTTP 500 with a JSON body.
func WriteInternalError(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusInternalServerError, errorBody{
		Error:  "internal-error",
		Reason: reason,
	})
}

// WriteConflict writes HTTP 409 with a JSON body. Used when a resume request
// cannot be applied in the current state (terminal instance, step not parked,
// user task already cancelled by a boundary event, etc.).
func WriteConflict(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusConflict, errorBody{
		Error:  "conflict",
		Reason: reason,
	})
}

// GRPCUnsupported returns a gRPC UNIMPLEMENTED status for unsupported operations.
func GRPCUnsupported(reason string) error {
	return status.Errorf(codes.Unimplemented, "%s", reason)
}

// GRPCInvalidArgument wraps a reason in a gRPC INVALID_ARGUMENT status.
func GRPCInvalidArgument(reason string) error {
	return status.Errorf(codes.InvalidArgument, "%s", reason)
}

// GRPCNotFound wraps a reason in a gRPC NOT_FOUND status.
func GRPCNotFound(reason string) error {
	return status.Errorf(codes.NotFound, "%s", reason)
}

// GRPCInternal wraps an error in a gRPC INTERNAL status.
func GRPCInternal(err error) error {
	return status.Errorf(codes.Internal, "internal error: %v", err)
}

// GRPCFailedPrecondition wraps a reason in a gRPC FAILED_PRECONDITION status.
// Used when a resume request targets an instance / step in an incompatible state.
func GRPCFailedPrecondition(reason string) error {
	return status.Errorf(codes.FailedPrecondition, "%s", reason)
}

// writeJSON marshals v and writes it as an application/json response.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
