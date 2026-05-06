// Package response provides helpers for writing structured HTTP responses.
package response

import (
	"encoding/json"
	"log/slog"
	"net/http"

	apierrors "github.com/topcheer/ggcode/examples/rest-api/errors"
)

// Response is the standard envelope for all API responses.
type Response struct {
	Success bool       `json:"success"`
	Data    any        `json:"data,omitempty"`
	Error   *ErrorBody `json:"error,omitempty"`
	Meta    *Meta      `json:"meta,omitempty"`
}

// ErrorBody is the error payload included when success is false.
type ErrorBody struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Details []apierrors.FieldError `json:"details,omitempty"`
}

// Meta carries pagination and request metadata.
type Meta struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// JSON writes a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	resp := Response{
		Success: status >= 200 && status < 300,
		Data:    data,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}

// Paginated writes a paginated JSON response with metadata.
func Paginated(w http.ResponseWriter, data any, page, perPage, total int) {
	totalPages := total / perPage
	if total%perPage > 0 {
		totalPages++
	}
	if totalPages == 0 {
		totalPages = 1
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	resp := Response{
		Success: true,
		Data:    data,
		Meta: &Meta{
			Page:       page,
			PerPage:    perPage,
			Total:      total,
			TotalPages: totalPages,
		},
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode paginated response", "error", err)
	}
}

// Error writes a structured error response from an APIError.
func Error(w http.ResponseWriter, err error) {
	if apiErr, ok := err.(*apierrors.APIError); ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(apiErr.Status)

		resp := Response{
			Success: false,
			Error: &ErrorBody{
				Code:    string(apiErr.Code),
				Message: apiErr.Message,
				Details: apiErr.Details,
			},
		}

		if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
			slog.Error("failed to encode error response", "error", encErr)
		}
		return
	}

	// Unexpected error — wrap as internal.
	slog.Error("unhandled error", "error", err)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)

	resp := Response{
		Success: false,
		Error: &ErrorBody{
			Code:    string(apierrors.ErrInternal),
			Message: "an unexpected error occurred",
		},
	}

	if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
		slog.Error("failed to encode error response", "error", encErr)
	}
}
