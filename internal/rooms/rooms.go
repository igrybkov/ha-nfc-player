// Package rooms resolves an HA area to its player entities and volume.
//
// Owner: track A (docs/PLAN.md section 10).
package rooms

import (
	"context"

	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// Area is an HA area with resolved players. Empty entity = none in the area.
type Area struct {
	ID          string
	Name        string
	SonosEntity string
	MAEntity    string
	Volume      int // area volume from settings, else rooms.default_volume
}

// Resolver maps areas and devices to players using the HA registries and settings.
type Resolver struct {
	ha       ha.Client
	settings model.SettingsSource
}

// New returns a Resolver.
func New(c ha.Client, s model.SettingsSource) *Resolver {
	return &Resolver{ha: c, settings: s}
}

// Area resolves one area by HA area_id.
func (r *Resolver) Area(ctx context.Context, areaID string) (Area, error) {
	_, _ = r.ha, r.settings
	panic("TODO: track A")
}

// Areas resolves all HA areas.
func (r *Resolver) Areas(ctx context.Context) ([]Area, error) { panic("TODO: track A") }

// DeviceArea returns the area_id of a device, and whether the device belongs
// to one of the given integrations (settings readers.integrations).
func (r *Resolver) DeviceArea(ctx context.Context, deviceID string, integrations []string) (areaID string, allowed bool, err error) {
	panic("TODO: track A")
}

// Volume picks the volume: entry override, else area volume.
func Volume(e model.Entry, a Area) int { panic("TODO: track A") }
