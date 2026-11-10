// Package hatest provides an in-memory ha.Client for tests.
//
// Owner: track A (docs/PLAN.md section 10).
package hatest

import (
	"context"
	"encoding/json"

	"github.com/igrybkov/ha-nfc-player/internal/ha"
)

// ServiceCall is one recorded CallService call.
type ServiceCall struct {
	Domain, Service string
	Data            map[string]any
	Target          *ha.Target
	ReturnResponse  bool
}

// Fake is an in-memory ha.Client. Set the registry fields before use; read
// Calls to assert the service call sequence. Emit delivers an event to
// subscribers.
type Fake struct {
	AreaList   []ha.Area
	DeviceList []ha.Device
	EntityList []ha.Entity
	StateList  []ha.State
	TagList    []ha.Tag
	Calls      []ServiceCall

	// Respond, if set, returns the response and error for a service call.
	Respond func(ServiceCall) (json.RawMessage, error)
}

var _ ha.Client = (*Fake)(nil)

// Emit delivers ev to every subscriber of ev.EventType.
func (f *Fake) Emit(ev ha.Event) { panic("TODO: track A") }

func (f *Fake) SubscribeEvents(ctx context.Context, eventType string, fn func(ha.Event)) error {
	panic("TODO: track A")
}

func (f *Fake) CallService(ctx context.Context, domain, service string, data map[string]any, target *ha.Target, returnResponse bool) (json.RawMessage, error) {
	panic("TODO: track A")
}

func (f *Fake) Areas(ctx context.Context) ([]ha.Area, error)      { return f.AreaList, nil }
func (f *Fake) Devices(ctx context.Context) ([]ha.Device, error)  { return f.DeviceList, nil }
func (f *Fake) Entities(ctx context.Context) ([]ha.Entity, error) { return f.EntityList, nil }
func (f *Fake) States(ctx context.Context) ([]ha.State, error)    { return f.StateList, nil }
func (f *Fake) Tags(ctx context.Context) ([]ha.Tag, error)        { return f.TagList, nil }
