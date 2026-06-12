// Package validator provides validation logic for model inputs.
package validator

import (
	"math"
	"strings"

	"github.com/topcheer/ggcode/examples/rest-api/errors"
	"github.com/topcheer/ggcode/examples/rest-api/model"
)

const (
	maxNameLength        = 200
	maxDescriptionLength = 2000
	maxSKULength         = 50
	maxTagLength         = 50
	maxTags              = 20
	maxPrice             = 10_000_000
)

// ValidateCreate validates a CreateProductInput and returns field errors.
func ValidateCreate(input *model.CreateProductInput) error {
	var fields []errors.FieldError

	// Name
	input.Name = strings.TrimSpace(input.Name)
	switch {
	case input.Name == "":
		fields = append(fields, errors.FieldError{Field: "name", Message: "name is required"})
	case len(input.Name) > maxNameLength:
		fields = append(fields, errors.FieldError{
			Field:   "name",
			Message: "name must be at most 200 characters",
		})
	}

	// Description
	if len(input.Description) > maxDescriptionLength {
		fields = append(fields, errors.FieldError{
			Field:   "description",
			Message: "description must be at most 2000 characters",
		})
	}

	// Price
	switch {
	case input.Price <= 0:
		fields = append(fields, errors.FieldError{
			Field:   "price",
			Message: "price must be greater than 0",
		})
	case input.Price > maxPrice:
		fields = append(fields, errors.FieldError{
			Field:   "price",
			Message: "price must be at most 10000000",
		})
	case math.IsNaN(input.Price) || math.IsInf(input.Price, 0):
		fields = append(fields, errors.FieldError{
			Field:   "price",
			Message: "price must be a valid number",
		})
	}

	// Currency
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	switch {
	case input.Currency == "":
		fields = append(fields, errors.FieldError{
			Field:   "currency",
			Message: "currency is required",
		})
	case len(input.Currency) != 3:
		fields = append(fields, errors.FieldError{
			Field:   "currency",
			Message: "currency must be a 3-letter ISO 4217 code",
		})
	}

	// SKU
	input.SKU = strings.TrimSpace(input.SKU)
	switch {
	case input.SKU == "":
		fields = append(fields, errors.FieldError{
			Field:   "sku",
			Message: "sku is required",
		})
	case len(input.SKU) > maxSKULength:
		fields = append(fields, errors.FieldError{
			Field:   "sku",
			Message: "sku must be at most 50 characters",
		})
	case !isAlphanumeric(input.SKU):
		fields = append(fields, errors.FieldError{
			Field:   "sku",
			Message: "sku must contain only alphanumeric characters and hyphens",
		})
	}

	// Tags
	if len(input.Tags) > maxTags {
		fields = append(fields, errors.FieldError{
			Field:   "tags",
			Message: "maximum of 20 tags allowed",
		})
	}
	for i, tag := range input.Tags {
		tag = strings.TrimSpace(tag)
		input.Tags[i] = tag
		switch {
		case tag == "":
			fields = append(fields, errors.FieldError{
				Field:   "tags",
				Message: "tag must not be empty",
			})
		case len(tag) > maxTagLength:
			fields = append(fields, errors.FieldError{
				Field:   "tags",
				Message: "each tag must be at most 50 characters",
			})
		}
	}

	if len(fields) > 0 {
		return errors.NewValidation("validation failed", fields)
	}
	return nil
}

// ValidateUpdate validates an UpdateProductInput. Only provided fields are checked.
func ValidateUpdate(input *model.UpdateProductInput) error {
	var fields []errors.FieldError

	if input.Name != nil {
		trimmed := strings.TrimSpace(*input.Name)
		input.Name = &trimmed
		switch {
		case *input.Name == "":
			fields = append(fields, errors.FieldError{
				Field: "name", Message: "name must not be empty",
			})
		case len(*input.Name) > maxNameLength:
			fields = append(fields, errors.FieldError{
				Field: "name", Message: "name must be at most 200 characters",
			})
		}
	}

	if input.Description != nil && len(*input.Description) > maxDescriptionLength {
		fields = append(fields, errors.FieldError{
			Field: "description", Message: "description must be at most 2000 characters",
		})
	}

	if input.Price != nil {
		switch {
		case *input.Price <= 0:
			fields = append(fields, errors.FieldError{
				Field: "price", Message: "price must be greater than 0",
			})
		case *input.Price > maxPrice:
			fields = append(fields, errors.FieldError{
				Field: "price", Message: "price must be at most 10000000",
			})
		case math.IsNaN(*input.Price) || math.IsInf(*input.Price, 0):
			fields = append(fields, errors.FieldError{
				Field: "price", Message: "price must be a valid number",
			})
		}
	}

	if input.Currency != nil {
		cur := strings.ToUpper(strings.TrimSpace(*input.Currency))
		input.Currency = &cur
		if len(cur) != 3 {
			fields = append(fields, errors.FieldError{
				Field: "currency", Message: "currency must be a 3-letter ISO 4217 code",
			})
		}
	}

	if input.SKU != nil {
		sku := strings.TrimSpace(*input.SKU)
		input.SKU = &sku
		switch {
		case *input.SKU == "":
			fields = append(fields, errors.FieldError{
				Field: "sku", Message: "sku must not be empty",
			})
		case len(*input.SKU) > maxSKULength:
			fields = append(fields, errors.FieldError{
				Field: "sku", Message: "sku must be at most 50 characters",
			})
		case !isAlphanumeric(*input.SKU):
			fields = append(fields, errors.FieldError{
				Field: "sku", Message: "sku must contain only alphanumeric characters and hyphens",
			})
		}
	}

	if input.Status != nil {
		if !model.ValidStatuses[*input.Status] {
			fields = append(fields, errors.FieldError{
				Field: "status", Message: "status must be one of: active, inactive, archived",
			})
		}
	}

	if input.Tags != nil {
		if len(*input.Tags) > maxTags {
			fields = append(fields, errors.FieldError{
				Field: "tags", Message: "maximum of 20 tags allowed",
			})
		}
		for _, tag := range *input.Tags {
			if tag == "" {
				fields = append(fields, errors.FieldError{
					Field: "tags", Message: "tag must not be empty",
				})
			} else if len(tag) > maxTagLength {
				fields = append(fields, errors.FieldError{
					Field: "tags", Message: "each tag must be at most 50 characters",
				})
			}
		}
	}

	if len(fields) > 0 {
		return errors.NewValidation("validation failed", fields)
	}
	return nil
}

// isAlphanumeric checks whether the string contains only letters, digits, and hyphens.
func isAlphanumeric(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}
