package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrNotFound is returned when a binding set is not present in a Repository.
var ErrNotFound = errors.New("binding set not found")

// Repository persists BindingSets. The in-memory implementation is the default
// for A6; a later wave (A8) may swap in a SQLite-backed implementation without
// changing callers. Repository implementations must copy on save and on load so
// callers cannot mutate stored state.
type Repository interface {
	// SaveBindingSet upserts a binding set for an application instance.
	SaveBindingSet(ctx context.Context, bs BindingSet) error
	// LoadBindingSet returns a binding set by application and plugin instance id.
	LoadBindingSet(ctx context.Context, applicationID, pluginInstanceID string) (BindingSet, error)
	// DeleteBindingSet removes a binding set.
	DeleteBindingSet(ctx context.Context, applicationID, pluginInstanceID string) error
	// ListBindingSets returns every binding set for a tenant (or all when tenant
	// is empty).
	ListBindingSets(ctx context.Context, tenantID string) ([]BindingSet, error)
}

// memoryRepository is an in-process, thread-safe Repository implementation.
type memoryRepository struct {
	mu   sync.RWMutex
	data map[string]BindingSet
}

// NewMemoryRepository returns a fresh in-memory Repository.
func NewMemoryRepository() Repository {
	return &memoryRepository{data: map[string]BindingSet{}}
}

func bindingKey(applicationID, pluginInstanceID string) string {
	return applicationID + "/" + pluginInstanceID
}

// SaveBindingSet upserts a deep copy of bs.
func (r *memoryRepository) SaveBindingSet(ctx context.Context, bs BindingSet) error {
	if bs.ApplicationID == "" || bs.PluginInstanceID == "" {
		return fmt.Errorf("repository: binding set requires application_id and plugin_instance_id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[bindingKey(bs.ApplicationID, bs.PluginInstanceID)] = cloneBindingSet(bs)
	return nil
}

// LoadBindingSet returns a deep copy of the stored set.
func (r *memoryRepository) LoadBindingSet(ctx context.Context, applicationID, pluginInstanceID string) (BindingSet, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bs, ok := r.data[bindingKey(applicationID, pluginInstanceID)]
	if !ok {
		return BindingSet{}, fmt.Errorf("%w: %s/%s", ErrNotFound, applicationID, pluginInstanceID)
	}
	return cloneBindingSet(bs), nil
}

// DeleteBindingSet removes a set; it is a no-op when the set is absent.
func (r *memoryRepository) DeleteBindingSet(ctx context.Context, applicationID, pluginInstanceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, bindingKey(applicationID, pluginInstanceID))
	return nil
}

// ListBindingSets returns deep copies of every set in a tenant.
func (r *memoryRepository) ListBindingSets(ctx context.Context, tenantID string) ([]BindingSet, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]BindingSet, 0, len(r.data))
	for _, bs := range r.data {
		if tenantID == "" || bs.TenantID == tenantID {
			out = append(out, cloneBindingSet(bs))
		}
	}
	return out, nil
}

func cloneBindingSet(bs BindingSet) BindingSet {
	out := bs
	if bs.Bindings != nil {
		out.Bindings = make([]Binding, len(bs.Bindings))
		copy(out.Bindings, bs.Bindings)
	}
	return out
}
