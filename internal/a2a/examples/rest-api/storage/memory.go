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

type ProductStore struct {
	mu       sync.RWMutex
	products map[string]*model.Product
	bySKU    map[string]string
	nextID   int
}

func NewProductStore() *ProductStore {
	return &ProductStore{
		products: make(map[string]*model.Product),
		bySKU:    make(map[string]string),
		nextID:   1,
	}
}

func (s *ProductStore) generateID() string {
	s.nextID++
	return fmt.Sprintf("prod_%d", s.nextID)
}

func (s *ProductStore) Create(input *model.CreateProductInput) (*model.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
		Tags:        append([]string(nil), input.Tags...),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	s.products[product.ID] = product
	s.bySKU[normalizedSKU] = product.ID

	return cloneProduct(product), nil
}

func cloneProduct(p *model.Product) *model.Product {
	cp := *p
	if p.Tags != nil {
		cp.Tags = append([]string(nil), p.Tags...)
	}
	return &cp
}

func (s *ProductStore) GetByID(id string) (*model.Product, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	product, exists := s.products[id]
	if !exists {
		return nil, errors.NewNotFound("product", id)
	}
	return cloneProduct(product), nil
}

func (s *ProductStore) Update(id string, input *model.UpdateProductInput) (*model.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	product, exists := s.products[id]
	if !exists {
		return nil, errors.NewNotFound("product", id)
	}

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
		product.Tags = append([]string(nil), (*input.Tags)...)
	}

	product.UpdatedAt = time.Now().UTC()
	return cloneProduct(product), nil
}

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

func (s *ProductStore) List(filter *model.ListProductsFilter) ([]*model.Product, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filter.ApplyDefaults()

	var result []*model.Product
	for _, p := range s.products {
		if !matchesFilter(p, filter) {
			continue
		}
		result = append(result, cloneProduct(p))
	}

	total := len(result)

	sortProducts(result, filter.SortBy, filter.SortDir)

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

func (s *ProductStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.products)
}

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

func sortProducts(products []*model.Product, sortBy, sortDir string) {
	asc := func(i, j int) bool {
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
		return less
	}
	var less func(i, j int) bool
	if sortDir == "desc" {
		less = func(i, j int) bool { return asc(j, i) }
	} else {
		less = asc
	}
	sort.Slice(products, less)
}
