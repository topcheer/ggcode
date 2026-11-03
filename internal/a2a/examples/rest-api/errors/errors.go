package errors

import (
	"fmt"
	"net/http"
)

type ErrorCode string

const (
	ErrNotFound         ErrorCode = "NOT_FOUND"
	ErrValidation       ErrorCode = "VALIDATION_ERROR"
	ErrBadRequest       ErrorCode = "BAD_REQUEST"
	ErrConflict         ErrorCode = "CONFLICT"
	ErrInternal         ErrorCode = "INTERNAL_ERROR"
	ErrRateLimit        ErrorCode = "RATE_LIMITED"
	ErrMethodNotAllowed ErrorCode = "METHOD_NOT_ALLOWED"
)

type APIError struct {
	Code    ErrorCode    `json:"code"`
	Message string       `json:"message"`
	Details []FieldError `json:"details,omitempty"`
	Status  int          `json:"-"`
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	if len(e.Details) > 0 {
		return fmt.Sprintf("%s: %s (%d field errors)", e.Code, e.Message, len(e.Details))
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewNotFound(resource, id string) *APIError {
	return &APIError{
		Code:    ErrNotFound,
		Message: fmt.Sprintf("%s with id %q not found", resource, id),
		Status:  http.StatusNotFound,
	}
}

func NewValidation(message string, details []FieldError) *APIError {
	return &APIError{
		Code:    ErrValidation,
		Message: message,
		Details: details,
		Status:  http.StatusUnprocessableEntity,
	}
}

func NewBadRequest(message string) *APIError {
	return &APIError{
		Code:    ErrBadRequest,
		Message: message,
		Status:  http.StatusBadRequest,
	}
}

func NewConflict(resource, field, value string) *APIError {
	return &APIError{
		Code:    ErrConflict,
		Message: fmt.Sprintf("%s with %s %q already exists", resource, field, value),
		Status:  http.StatusConflict,
	}
}

func NewInternal(message string) *APIError {
	return &APIError{
		Code:    ErrInternal,
		Message: message,
		Status:  http.StatusInternalServerError,
	}
}

func NewRateLimit(message string) *APIError {
	return &APIError{
		Code:    ErrRateLimit,
		Message: message,
		Status:  http.StatusTooManyRequests,
	}
}

func NewMethodNotAllowed(method string) *APIError {
	return &APIError{
		Code:    ErrMethodNotAllowed,
		Message: fmt.Sprintf("method %q is not allowed", method),
		Status:  http.StatusMethodNotAllowed,
	}
}
