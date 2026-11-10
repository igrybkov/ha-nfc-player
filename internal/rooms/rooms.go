// Package rooms resolves an HA area to its player entities and volume.
//
// Owner: track A (docs/PLAN.md section 10).
package rooms

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// Integration domains of the players.
const (
	SonosPlatform          = "sonos"
	MusicAssistantPlatform = "music_assistant"
)

// FallbackVolume is used when settings have no usable rooms.default_volume.
const FallbackVolume = 15

// ErrUnknownArea means HA has no area with that id.
var ErrUnknownArea = errors.New("unknown area")

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

// registry is one snapshot of the HA registries used for resolving.
type registry struct {
	areas    []ha.Area
	devices  map[string]ha.Device
	entities []ha.Entity
}

func (r *Resolver) load(ctx context.Context, withAreas bool) (registry, error) {
	var reg registry
	if withAreas {
		areas, err := r.ha.Areas(ctx)
		if err != nil {
			return reg, err
		}
		reg.areas = areas
	}
	devices, err := r.ha.Devices(ctx)
	if err != nil {
		return reg, err
	}
	reg.devices = make(map[string]ha.Device, len(devices))
	for _, d := range devices {
		reg.devices[d.ID] = d
	}
	reg.entities, err = r.ha.Entities(ctx)
	return reg, err
}

// entityArea is the area of an entity: its own override, else its device's.
func (reg registry) entityArea(e ha.Entity) string {
	if e.AreaID != "" {
		return e.AreaID
	}
	return reg.devices[e.DeviceID].AreaID
}

// Area resolves one area by HA area_id.
func (r *Resolver) Area(ctx context.Context, areaID string) (Area, error) {
	reg, err := r.load(ctx, true)
	if err != nil {
		return Area{}, err
	}
	for _, a := range reg.areas {
		if a.AreaID == areaID {
			return r.resolve(reg, a, r.settings.Settings()), nil
		}
	}
	return Area{}, fmt.Errorf("%w: %q", ErrUnknownArea, areaID)
}

// Areas resolves all HA areas, in registry order.
func (r *Resolver) Areas(ctx context.Context) ([]Area, error) {
	reg, err := r.load(ctx, true)
	if err != nil {
		return nil, err
	}
	s := r.settings.Settings()
	out := make([]Area, 0, len(reg.areas))
	for _, a := range reg.areas {
		out = append(out, r.resolve(reg, a, s))
	}
	return out, nil
}

func (r *Resolver) resolve(reg registry, a ha.Area, s model.Settings) Area {
	as := s.Rooms.Areas[a.AreaID]
	out := Area{ID: a.AreaID, Name: a.Name, Volume: s.Rooms.DefaultVolume}
	if out.Volume <= 0 {
		out.Volume = FallbackVolume
	}
	if as.Volume != nil {
		out.Volume = *as.Volume
	}

	sonos := reg.players(a.AreaID, SonosPlatform)
	out.SonosEntity = pickSonos(reg, sonos, s.Rooms.SonosModelPreference)
	if as.SonosEntity != nil && *as.SonosEntity != "" {
		out.SonosEntity = *as.SonosEntity
	}
	if ma := reg.players(a.AreaID, MusicAssistantPlatform); len(ma) > 0 {
		out.MAEntity = ma[0].EntityID
	}
	if as.MAEntity != nil && *as.MAEntity != "" {
		out.MAEntity = *as.MAEntity
	}
	return out
}

// players returns the enabled media_player entities of one integration in
// an area, sorted by entity_id so the pick is stable.
func (reg registry) players(areaID, platform string) []ha.Entity {
	var out []ha.Entity
	for _, e := range reg.entities {
		if e.Platform == platform && e.DisabledBy == "" &&
			strings.HasPrefix(e.EntityID, "media_player.") && reg.entityArea(e) == areaID {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b ha.Entity) int { return strings.Compare(a.EntityID, b.EntityID) })
	return out
}

// pickSonos takes the first entity whose device model is in the preference
// list, in list order, else the first entity. Models compare exactly but
// ignoring case, like is_device_attr in the old script.
func pickSonos(reg registry, candidates []ha.Entity, preference []string) string {
	for _, want := range preference {
		for _, e := range candidates {
			if strings.EqualFold(reg.devices[e.DeviceID].Model, want) {
				return e.EntityID
			}
		}
	}
	if len(candidates) > 0 {
		return candidates[0].EntityID
	}
	return ""
}

// DeviceArea returns the area_id of a device, and whether the device belongs
// to one of the given integrations (settings readers.integrations).
//
// A device belongs to an integration when it has an entity of that platform.
// The area is the device area, else the area of the first entity that has
// one. An unknown device is not allowed.
func (r *Resolver) DeviceArea(ctx context.Context, deviceID string, integrations []string) (areaID string, allowed bool, err error) {
	if deviceID == "" {
		return "", false, nil
	}
	reg, err := r.load(ctx, false)
	if err != nil {
		return "", false, err
	}
	d, ok := reg.devices[deviceID]
	if !ok {
		return "", false, nil
	}
	areaID = d.AreaID
	for _, e := range reg.entities {
		if e.DeviceID != deviceID {
			continue
		}
		if slices.Contains(integrations, e.Platform) {
			allowed = true
		}
		if areaID == "" && e.AreaID != "" {
			areaID = e.AreaID
		}
	}
	return areaID, allowed, nil
}

// Volume picks the volume: entry override, else area volume. The result is
// clamped to 0..100 percent.
func Volume(e model.Entry, a Area) int {
	v := a.Volume
	if e.Volume != nil {
		v = *e.Volume
	}
	return min(max(v, 0), 100)
}
