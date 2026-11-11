package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/igrybkov/ha-nfc-player/internal/labels"
	"github.com/igrybkov/ha-nfc-player/internal/records"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// Errors of the template and theme stores.
var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("duplicate id")
	ErrReadOnly  = errors.New("built-in, read-only")
)

// TemplatesFile is the content of templates.yaml: the user templates.
type TemplatesFile struct {
	Version   int               `yaml:"version" json:"version"`
	Templates []labels.Template `yaml:"templates" json:"templates"`
}

// DefaultTemplatesFile is the content of a new templates.yaml.
func DefaultTemplatesFile() TemplatesFile { return TemplatesFile{Templates: []labels.Template{}} }

// ThemesFile is the content of themes.yaml: the user themes.
type ThemesFile struct {
	Version int            `yaml:"version" json:"version"`
	Themes  []labels.Theme `yaml:"themes" json:"themes"`
}

// DefaultThemesFile is the content of a new themes.yaml.
func DefaultThemesFile() ThemesFile { return ThemesFile{Themes: []labels.Theme{}} }

// TemplateStore serves label templates: the embedded built-ins plus the user
// templates in templates.yaml. Built-ins are read-only.
type TemplateStore struct {
	catalog[TemplatesFile, labels.Template]
}

// NewTemplateStore returns a TemplateStore on f (user templates only).
// Open f with DefaultTemplatesFile as the default.
func NewTemplateStore(f *store.File[TemplatesFile]) (*TemplateStore, error) {
	builtins, err := labels.BuiltinTemplates()
	if err != nil {
		return nil, fmt.Errorf("built-in templates: %w", err)
	}
	return &TemplateStore{catalog[TemplatesFile, labels.Template]{
		file:     f,
		list:     func(f *TemplatesFile) *[]labels.Template { return &f.Templates },
		builtins: builtins,
		fields:   func(t *labels.Template) (*string, *string, *bool) { return &t.ID, &t.Name, &t.Builtin },
		validate: labels.Template.Validate,
	}}, nil
}

// ThemeStore serves label themes: the embedded built-ins plus the user
// themes in themes.yaml. Built-ins are read-only.
type ThemeStore struct {
	catalog[ThemesFile, labels.Theme]
}

// NewThemeStore returns a ThemeStore on f (user themes only). Open f with
// DefaultThemesFile as the default.
func NewThemeStore(f *store.File[ThemesFile]) (*ThemeStore, error) {
	builtins, err := labels.BuiltinThemes()
	if err != nil {
		return nil, fmt.Errorf("built-in themes: %w", err)
	}
	return &ThemeStore{catalog[ThemesFile, labels.Theme]{
		file:     f,
		list:     func(f *ThemesFile) *[]labels.Theme { return &f.Themes },
		builtins: builtins,
		fields:   func(b *labels.Theme) (*string, *string, *bool) { return &b.ID, &b.Name, &b.Builtin },
		validate: labels.Theme.Validate,
	}}, nil
}

// catalog is the shared logic of TemplateStore and ThemeStore. F is
// the file type; list points at its user items.
type catalog[F, T any] struct {
	file     *store.File[F]
	list     func(*F) *[]T
	builtins []T
	fields   func(*T) (id, name *string, builtin *bool)
	validate func(T) error // labels' Validate; tests swap it
}

func (c *catalog[F, T]) id(v T) string { id, _, _ := c.fields(&v); return *id }

// List returns the built-ins first, then the user items in file order.
// A read error comes with the last good user items.
func (c *catalog[F, T]) List() ([]T, error) {
	user, err := c.user()
	out := make([]T, 0, len(c.builtins)+len(user))
	out = append(out, c.builtins...)
	return append(out, user...), err
}

// Get returns an item by id, built-in or user.
func (c *catalog[F, T]) Get(id string) (T, error) {
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
func (c *catalog[F, T]) Create(v T) (T, error) {
	id, name, builtin := c.fields(&v)
	*id, *name, *builtin = strings.TrimSpace(*id), strings.TrimSpace(*name), false
	if *name == "" {
		return v, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	err := c.update(func(user *[]T) error {
		if *id == "" {
			*id = c.uniqueID(records.Slug(*name), *user)
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
func (c *catalog[F, T]) Update(id string, v T) (T, error) {
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
	err := c.update(func(user *[]T) error {
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
func (c *catalog[F, T]) Delete(id string) error {
	if c.isBuiltin(id) {
		return fmt.Errorf("%w: %s", ErrReadOnly, id)
	}
	return c.update(func(user *[]T) error {
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
func (c *catalog[F, T]) user() ([]T, error) {
	f, err := c.file.Load()
	items := *c.list(&f)
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

// update runs fn on the user items under the file lock.
func (c *catalog[F, T]) update(fn func(user *[]T) error) error {
	return c.file.Update(func(f *F) error { return fn(c.list(f)) })
}

func (c *catalog[F, T]) check(v T) error {
	if err := c.validate(v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

func (c *catalog[F, T]) isBuiltin(id string) bool {
	return slices.ContainsFunc(c.builtins, func(b T) bool { return c.id(b) == id })
}

func (c *catalog[F, T]) taken(id string, user []T) bool {
	return c.isBuiltin(id) || slices.ContainsFunc(user, func(u T) bool { return c.id(u) == id })
}

func (c *catalog[F, T]) uniqueID(base string, user []T) string {
	id := base
	for n := 2; c.taken(id, user); n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	return id
}
