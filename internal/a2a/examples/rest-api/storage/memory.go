// Package storage provides an in-memory, concurrency-safe data store for products.
package storage

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/examples/rest-api/errors"
	"github.com/topcheer/ggcode/examples/rest-api/model"
)

// ProductStore manages products in memory with full concurrency safety.
type ProductStore struct {
	mu       sync.RWMutex
	products map[string]*model.Product
	bySKU    map[string]string // sku -> id lookup
	nextID   int
}

// NewProductStore creates a new empty product store.
func NewProductStore() *ProductStore {
	return &ProductStore{
		products: make(map[string]*model.Product),
		bySKU:    make(map[string]string),
		nextID:   1,
	}
}

// generateID produces a unique product ID.
func (s *ProductStore) generateID() string {
	s.nextID++
	return fmt.Sprintf("prod_%d", s.nextID)
}

// Create adds a new product to the store. Returns an error if the SKU is already taken.
func (s *ProductStore) Create(input *model.CreateProductInput) (*model.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check SKU uniqueness.
	normalizedSKU := strings.ToUpper(strings.TrimSpace(input.SKU))
	if _, exists := s.bySKU[normalizedSKU]; exists {
		return nil, errors.NewConflict("product", "sku", normalizedSKU)
	}

	now := time.Now().UTC()
	product := &model.Product{
		ID:          s.generateID(),
		Name:        input.Name,
		Description: input.Description,
		Price:       input.Price,
		Currency:    input.Currency,
		SKU:         normalizedSKU,
		Status:      model.StatusActive,
		Tags:        input.Tags,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	s.products[product.ID] = product
	s.bySKU[normalizedSKU] = product.ID

	return product, nil
}

// GetByID retrieves a product by its ID.
func (s *ProductStore) GetByID(id string) (*model.Product, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	product, exists := s.products[id]
	if !exists {
		return nil, errors.NewNotFound("product", id)
	}
	return product, nil
}

// Update modifies an existing product. Only non-nil fields in the input are applied.
func (s *ProductStore) Update(id string, input *model.UpdateProductInput) (*model.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	product, exists := s.products[id]
	if !exists {
		return nil, errors.NewNotFound("product", id)
	}

	// If SKU is being changed, check uniqueness.
	if input.SKU != nil {
		newSKU := strings.ToUpper(strings.TrimSpace(*input.SKU))
		if newSKU != product.SKU {
			if existingID, found := s.bySKU[newSKU]; found && existingID != id {
				return nil, errors.NewConflict("product", "sku", newSKU)
			}
			delete(s.bySKU, product.SKU)
			product.SKU = newSKU
			s.bySKU[newSKU] = id
		}
	}

	// Apply non-nil updates.
	if input.Name != nil {
		product.Name = *input.Name
	}
	if input.Description != nil {
		product.Description = *input.Description
	}
	if input.Price != nil {
		product.Price = *input.Price
	}
	if input.Currency != nil {
		product.Currency = *input.Currency
	}
	if input.Status != nil {
		product.Status = *input.Status
	}
	if input.Tags != nil {
		product.Tags = *input.Tags
	}

	product.UpdatedAt = time.Now().UTC()
	return product, nil
}

// Delete removes a product from the store by ID.
func (s *ProductStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	product, exists := s.products[id]
	if !exists {
		return errors.NewNotFound("product", id)
	}

	delete(s.bySKU, product.SKU)
	delete(s.products, id)
	return nil
}

// List returns a filtered, sorted, and paginated slice of products.
func (s *ProductStore) List(filter *model.ListProductsFilter) ([]*model.Product, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filter.ApplyDefaults()

	// Collect all products and apply filters.
	var result []*model.Product
	for _, p := range s.products {
		if !matchesFilter(p, filter) {
			continue
		}
		result = append(result, p)
	}

	total := len(result)

	// Sort.
	sortProducts(result, filter.SortBy, filter.SortDir)

	// Paginate.
	start := (filter.Page - 1) * filter.PerPage
	if start >= total {
		return []*model.Product{}, total, nil
	}
	end := start + filter.PerPage
	if end > total {
		end = total
	}

	return result[start:end], total, nil
}

// Count returns the total number of products in the store.
func (s *ProductStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.products)
}

// matchesFilter checks if a product matches the given filter criteria.
func matchesFilter(p *model.Product, f *model.ListProductsFilter) bool {
	if f.Name != "" && !strings.Contains(
		strings.ToLower(p.Name),
		strings.ToLower(f.Name),
	) {
		return false
	}
	if f.Status != nil && p.Status != *f.Status {
		return false
	}
	if f.MinPrice != nil && p.Price < *f.MinPrice {
		return false
	}
	if f.MaxPrice != nil && p.Price > *f.MaxPrice {
		return false
	}
	if f.Tag != "" {
		found := false
		for _, tag := range p.Tags {
			if strings.EqualFold(tag, f.Tag) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// sortProducts sorts the slice in place according to sortBy and sortDir.
func sortProducts(products []*model.Product, sortBy, sortDir string) {
	less := func(i, j int) bool {
		var less bool
		switch sortBy {
		case "name":
			less = products[i].Name < products[j].Name
		case "price":
			less = products[i].Price < products[j].Price
		case "updated_at":
			less = products[i].UpdatedAt.Before(products[j].UpdatedAt)
		case "created_at":
			fallthrough
		default:
			less = products[i].CreatedAt.Before(products[j].CreatedAt)
		}
		if sortDir == "desc" {
			return !less
		}
		return less
	}
	sort.Slice(products, less)
}
