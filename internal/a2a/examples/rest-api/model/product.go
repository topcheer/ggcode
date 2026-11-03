package model

import (
	"strings"
	"time"
)

type ProductStatus string

const (
	StatusActive   ProductStatus = "active"
	StatusInactive ProductStatus = "inactive"
	StatusArchived ProductStatus = "archived"
)

var ValidStatuses = map[ProductStatus]bool{
	StatusActive:   true,
	StatusInactive: true,
	StatusArchived: true,
}

type Product struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Price       float64       `json:"price"`
	Currency    string        `json:"currency"`
	SKU         string        `json:"sku"`
	Status      ProductStatus `json:"status"`
	Tags        []string      `json:"tags,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

type CreateProductInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Price       float64  `json:"price"`
	Currency    string   `json:"currency"`
	SKU         string   `json:"sku"`
	Tags        []string `json:"tags"`
}

type UpdateProductInput struct {
	Name        *string        `json:"name,omitempty"`
	Description *string        `json:"description,omitempty"`
	Price       *float64       `json:"price,omitempty"`
	Currency    *string        `json:"currency,omitempty"`
	SKU         *string        `json:"sku,omitempty"`
	Status      *ProductStatus `json:"status,omitempty"`
	Tags        *[]string      `json:"tags,omitempty"`
}

type ListProductsFilter struct {
	Page     int            `json:"page"`
	PerPage  int            `json:"per_page"`
	Name     string         `json:"name,omitempty"`
	Status   *ProductStatus `json:"status,omitempty"`
	MinPrice *float64       `json:"min_price,omitempty"`
	MaxPrice *float64       `json:"max_price,omitempty"`
	Tag      string         `json:"tag,omitempty"`
	SortBy   string         `json:"sort_by,omitempty"`
	SortDir  string         `json:"sort_dir,omitempty"`
}

func (f *ListProductsFilter) ApplyDefaults() {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 || f.PerPage > 100 {
		f.PerPage = 20
	}
	if f.SortBy == "" {
		f.SortBy = "created_at"
	}
	if f.SortDir == "" {
		f.SortDir = "desc"
	}
}

func (i *CreateProductInput) Normalize() {
	i.Name = strings.TrimSpace(i.Name)
	i.Description = strings.TrimSpace(i.Description)
	i.Currency = strings.ToUpper(strings.TrimSpace(i.Currency))
	i.SKU = strings.ToUpper(strings.TrimSpace(i.SKU))
	for idx, tag := range i.Tags {
		i.Tags[idx] = strings.TrimSpace(tag)
	}
}

func (i *UpdateProductInput) Normalize() {
	if i.Name != nil {
		trimmed := strings.TrimSpace(*i.Name)
		i.Name = &trimmed
	}
	if i.Description != nil {
		trimmed := strings.TrimSpace(*i.Description)
		i.Description = &trimmed
	}
	if i.Currency != nil {
		trimmed := strings.ToUpper(strings.TrimSpace(*i.Currency))
		i.Currency = &trimmed
	}
	if i.SKU != nil {
		trimmed := strings.ToUpper(strings.TrimSpace(*i.SKU))
		i.SKU = &trimmed
	}
	if i.Tags != nil {
		for idx, tag := range *i.Tags {
			(*i.Tags)[idx] = strings.TrimSpace(tag)
		}
	}
}
