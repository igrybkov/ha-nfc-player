package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/igrybkov/ha-nfc-player/internal/labels"
	"github.com/igrybkov/ha-nfc-player/internal/registry"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// Errors of the template and background stores.
var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("duplicate id")
	ErrReadOnly  = errors.New("built-in, read-only")
)

// TemplateStore serves label templates: the embedded built-ins plus the user
// templates in templates.yaml. Built-ins are read-only.
type TemplateStore struct {
	catalog[labels.Template]
}

// NewTemplateStore returns a TemplateStore on f (user templates only).
// Open f with an empty-list default.
func NewTemplateStore(f *store.File[[]labels.Template]) (*TemplateStore, error) {
	builtins, err := labels.BuiltinTemplates()
	if err != nil {
		return nil, fmt.Errorf("built-in templates: %w", err)
	}
	return &TemplateStore{catalog[labels.Template]{
		file:     f,
		builtins: builtins,
		fields:   func(t *labels.Template) (*string, *string, *bool) { return &t.ID, &t.Name, &t.Builtin },
		validate: labels.Template.Validate,
	}}, nil
}

// BackgroundStore serves label backgrounds: the embedded built-ins plus the
// user backgrounds in backgrounds.yaml. Built-ins are read-only.
type BackgroundStore struct {
	catalog[labels.Background]
}

// NewBackgroundStore returns a BackgroundStore on f (user backgrounds only).
// Open f with an empty-list default.
func NewBackgroundStore(f *store.File[[]labels.Background]) (*BackgroundStore, error) {
	builtins, err := labels.BuiltinBackgrounds()
	if err != nil {
		return nil, fmt.Errorf("built-in backgrounds: %w", err)
	}
	return &BackgroundStore{catalog[labels.Background]{
		file:     f,
		builtins: builtins,
		fields:   func(b *labels.Background) (*string, *string, *bool) { return &b.ID, &b.Name, &b.Builtin },
		validate: labels.Background.Validate,
	}}, nil
}

// catalog is the shared logic of TemplateStore and BackgroundStore.
type catalog[T any] struct {
	file     *store.File[[]T]
	builtins []T
	fields   func(*T) (id, name *string, builtin *bool)
	validate func(T) error // labels' Validate; tests swap it
}

func (c *catalog[T]) id(v T) string { id, _, _ := c.fields(&v); return *id }

// List returns the built-ins first, then the user items in file order.
// A read error comes with the last good user items.
func (c *catalog[T]) List() ([]T, error) {
	user, err := c.user()
	out := make([]T, 0, len(c.builtins)+len(user))
	out = append(out, c.builtins...)
	return append(out, user...), err
}

// Get returns an item by id, built-in or user.
func (c *catalog[T]) Get(id string) (T, error) {
	all, err := c.List()
	if i := slices.IndexFunc(all, func(v T) bool { return c.id(v) == id }); i >= 0 {
		return all[i], nil
	}
	var zero T
	if err != nil {
		return zero, err
	}
	return zero, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Create stores a new user item. An empty id is made from the name.
func (c *catalog[T]) Create(v T) (T, error) {
	id, name, builtin := c.fields(&v)
	*id, *name, *builtin = strings.TrimSpace(*id), strings.TrimSpace(*name), false
	if *name == "" {
		return v, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	err := c.file.Update(func(user *[]T) error {
		if *id == "" {
			*id = c.uniqueID(registry.Slug(*name), *user)
		} else if c.taken(*id, *user) {
			return fmt.Errorf("%w: %s", ErrDuplicate, *id)
		}
		if err := c.check(v); err != nil {
			return err
		}
		*user = append(*user, v)
		return nil
	})
	return v, err
}

// Update replaces a user item. The id does not change.
func (c *catalog[T]) Update(id string, v T) (T, error) {
	if c.isBuiltin(id) {
		return v, fmt.Errorf("%w: %s", ErrReadOnly, id)
	}
	vid, name, builtin := c.fields(&v)
	if *vid != "" && *vid != id {
		return v, fmt.Errorf("%w: id cannot change (%q → %q)", ErrInvalid, id, *vid)
	}
	*vid, *name, *builtin = id, strings.TrimSpace(*name), false
	if *name == "" {
		return v, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if err := c.check(v); err != nil {
		return v, err
	}
	err := c.file.Update(func(user *[]T) error {
		i := slices.IndexFunc(*user, func(u T) bool { return c.id(u) == id })
		if i < 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		(*user)[i] = v
		return nil
	})
	return v, err
}

// Delete removes a user item.
func (c *catalog[T]) Delete(id string) error {
	if c.isBuiltin(id) {
		return fmt.Errorf("%w: %s", ErrReadOnly, id)
	}
	return c.file.Update(func(user *[]T) error {
		i := slices.IndexFunc(*user, func(u T) bool { return c.id(u) == id })
		if i < 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		*user = slices.Delete(*user, i, i+1)
		return nil
	})
}

// user loads the user items and drops any that collide with a built-in id
// (a hand edit); built-ins win. Builtin is forced to false.
func (c *catalog[T]) user() ([]T, error) {
	items, err := c.file.Load()
	out := items[:0]
	for _, v := range items {
		_, _, builtin := c.fields(&v)
		*builtin = false
		if !c.isBuiltin(c.id(v)) {
			out = append(out, v)
		}
	}
	return out, err
}

func (c *catalog[T]) check(v T) error {
	if err := c.validate(v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

func (c *catalog[T]) isBuiltin(id string) bool {
	return slices.ContainsFunc(c.builtins, func(b T) bool { return c.id(b) == id })
}

func (c *catalog[T]) taken(id string, user []T) bool {
	return c.isBuiltin(id) || slices.ContainsFunc(user, func(u T) bool { return c.id(u) == id })
}

func (c *catalog[T]) uniqueID(base string, user []T) string {
	id := base
	for n := 2; c.taken(id, user); n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	return id
}
