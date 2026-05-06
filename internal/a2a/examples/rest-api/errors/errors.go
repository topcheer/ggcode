// Package errors defines custom error types for the REST API.
package errors

import (
	"fmt"
	"net/http"
)

// ErrorCode is a machine-readable error identifier.
type ErrorCode string

const (
	// ErrNotFound indicates the requested resource does not exist.
	ErrNotFound ErrorCode = "NOT_FOUND"
	// ErrValidation indicates request data failed validation.
	ErrValidation ErrorCode = "VALIDATION_ERROR"
	// ErrBadRequest indicates the request is malformed.
	ErrBadRequest ErrorCode = "BAD_REQUEST"
	// ErrConflict indicates a duplicate or conflicting state.
	ErrConflict ErrorCode = "CONFLICT"
	// ErrInternal indicates an unexpected server error.
	ErrInternal ErrorCode = "INTERNAL_ERROR"
	// ErrRateLimit indicates the client has exceeded rate limits.
	ErrRateLimit ErrorCode = "RATE_LIMITED"
	// ErrMethodNotAllowed indicates the HTTP method is not supported.
	ErrMethodNotAllowed ErrorCode = "METHOD_NOT_ALLOWED"
)

// APIError is a structured error that can be returned in HTTP responses.
type APIError struct {
	Code    ErrorCode    `json:"code"`
	Message string       `json:"message"`
	Details []FieldError `json:"details,omitempty"`
	Status  int          `json:"-"`
}

// FieldError describes a validation error on a specific field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if len(e.Details) > 0 {
		return fmt.Sprintf("%s: %s (%d field errors)", e.Code, e.Message, len(e.Details))
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewNotFound returns a 404 error for the given resource type and ID.
func NewNotFound(resource, id string) *APIError {
	return &APIError{
		Code:    ErrNotFound,
		Message: fmt.Sprintf("%s with id %q not found", resource, id),
		Status:  http.StatusNotFound,
	}
}

// NewValidation returns a 422 error with field-level details.
func NewValidation(message string, details []FieldError) *APIError {
	return &APIError{
		Code:    ErrValidation,
		Message: message,
		Details: details,
		Status:  http.StatusUnprocessableEntity,
	}
}

// NewBadRequest returns a 400 error for malformed requests.
func NewBadRequest(message string) *APIError {
	return &APIError{
		Code:    ErrBadRequest,
		Message: message,
		Status:  http.StatusBadRequest,
	}
}

// NewConflict returns a 409 error for duplicate resources.
func NewConflict(resource, field, value string) *APIError {
	return &APIError{
		Code:    ErrConflict,
		Message: fmt.Sprintf("%s with %s %q already exists", resource, field, value),
		Status:  http.StatusConflict,
	}
}

// NewInternal returns a 500 error for unexpected failures.
func NewInternal(message string) *APIError {
	return &APIError{
		Code:    ErrInternal,
		Message: message,
		Status:  http.StatusInternalServerError,
	}
}

// NewRateLimit returns a 429 error.
func NewRateLimit(message string) *APIError {
	return &APIError{
		Code:    ErrRateLimit,
		Message: message,
		Status:  http.StatusTooManyRequests,
	}
}

// NewMethodNotAllowed returns a 405 error.
func NewMethodNotAllowed(method string) *APIError {
	return &APIError{
		Code:    ErrMethodNotAllowed,
		Message: fmt.Sprintf("method %q is not allowed", method),
		Status:  http.StatusMethodNotAllowed,
	}
}
