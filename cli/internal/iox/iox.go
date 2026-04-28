// Package iox provides the JSON envelope used by the archetipo CLI on stdin,
// stdout and stderr, plus typed errors that carry an exit code.
//
// Envelope shape on stdout (success):
//
//	{"schema":"archetipo/v1","kind":"...","data":{...}}
//
// Envelope shape on stderr (failure):
//
//	{"schema":"archetipo/v1","kind":"error","error":{"code":"E_*","message":"...","hint":"..."}}
//
// All payloads are versioned with the schema field so the consuming skill can
// react to breaking changes.
package iox

import (
	"encoding/json"
	"fmt"
	"io"
)

// Schema is the JSON envelope schema version emitted by every command.
const Schema = "archetipo/v1"

// Exit codes documented in contracts.md / plan.
const (
	ExitOK              = 0
	ExitGeneric         = 1
	ExitInvalidInput    = 2
	ExitConnector       = 3
	ExitPreconditionMissing = 4
)

// Envelope is the shape written to stdout for every successful command.
type Envelope struct {
	Schema string `json:"schema"`
	Kind   string `json:"kind"`
	Data   any    `json:"data,omitempty"`
}

// ErrorEnvelope is the shape written to stderr on failure.
type ErrorEnvelope struct {
	Schema string `json:"schema"`
	Kind   string `json:"kind"`
	Error  ErrorPayload `json:"error"`
}

// ErrorPayload is the structured error body. Code is one of the E_* constants.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Error codes. Skills branch on Code, not on Message.
const (
	CodeInvalidInput        = "E_INVALID_INPUT"
	CodeConnectorAuth       = "E_AUTH_SCOPE"
	CodeConnectorNetwork    = "E_NETWORK"
	CodeConnectorBackend    = "E_CONNECTOR"
	CodePreconditionMissing = "E_PRECONDITION"
	CodeNotFound            = "E_NOT_FOUND"
	CodeConflict            = "E_CONFLICT"
	CodeInternal            = "E_INTERNAL"
)

// CodedError is an error that carries both a stable code (for the JSON
// envelope) and an exit code (for the process). Use New* constructors.
type CodedError struct {
	Code    string
	Message string
	Hint    string
	Exit    int
	Cause   error
}

func (e *CodedError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// ExitCode satisfies the interface inspected by cli.Execute.
func (e *CodedError) ExitCode() int { return e.Exit }

// Unwrap exposes the underlying cause for errors.Is/As.
func (e *CodedError) Unwrap() error { return e.Cause }

// NewInvalidInput builds an error for malformed CLI args or stdin payloads.
func NewInvalidInput(message, hint string, cause error) *CodedError {
	return &CodedError{Code: CodeInvalidInput, Message: message, Hint: hint, Exit: ExitInvalidInput, Cause: cause}
}

// NewConnector builds an error for a connector-side failure (gh, GraphQL, fs).
func NewConnector(code, message, hint string, cause error) *CodedError {
	if code == "" {
		code = CodeConnectorBackend
	}
	return &CodedError{Code: code, Message: message, Hint: hint, Exit: ExitConnector, Cause: cause}
}

// NewPrecondition builds an error for a missing precondition (no backlog,
// auth scope missing, etc.) that the user must resolve before retrying.
func NewPrecondition(message, hint string, cause error) *CodedError {
	return &CodedError{Code: CodePreconditionMissing, Message: message, Hint: hint, Exit: ExitPreconditionMissing, Cause: cause}
}

// NewInternal wraps an unexpected error.
func NewInternal(message string, cause error) *CodedError {
	return &CodedError{Code: CodeInternal, Message: message, Exit: ExitGeneric, Cause: cause}
}

// NewNotFound builds an error for a referenced artifact (story, task, ...) that
// the connector could not locate.
func NewNotFound(message, hint string, cause error) *CodedError {
	return &CodedError{Code: CodeNotFound, Message: message, Hint: hint, Exit: ExitGeneric, Cause: cause}
}

// NewConflict builds an error when an operation cannot proceed from the current
// state of the artifact (e.g. `story start` on a TODO story).
func NewConflict(message, hint string, cause error) *CodedError {
	return &CodedError{Code: CodeConflict, Message: message, Hint: hint, Exit: ExitGeneric, Cause: cause}
}

// WriteOK marshals data as a success envelope on the given writer.
func WriteOK(w io.Writer, kind string, data any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(Envelope{Schema: Schema, Kind: kind, Data: data})
}

// WriteError marshals err as an error envelope on the given writer. Errors
// that are not *CodedError are coerced to E_INTERNAL.
func WriteError(w io.Writer, err error) {
	var ce *CodedError
	if c, ok := err.(*CodedError); ok {
		ce = c
	} else {
		ce = NewInternal(err.Error(), err)
	}
	env := ErrorEnvelope{
		Schema: Schema,
		Kind:   "error",
		Error:  ErrorPayload{Code: ce.Code, Message: ce.Message, Hint: ce.Hint},
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(env)
}

// ReadJSON decodes a single JSON value from r into v. Returns
// E_INVALID_INPUT on malformed input.
func ReadJSON(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return NewInvalidInput("invalid JSON on stdin", "expected schema "+Schema, err)
	}
	return nil
}
