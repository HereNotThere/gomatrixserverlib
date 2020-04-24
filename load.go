package gomatrixserverlib

import (
	"context"
	"encoding/json"
	"fmt"
)

// EventLoadResult is the result of loading and verifying an event in the EventsLoader.
type EventLoadResult struct {
	Event    *HeaderedEvent
	Error    error
	SoftFail bool
}

// EventsLoader loads untrusted events and verifies them.
type EventsLoader struct {
	roomVer       RoomVersion
	keyRing       JSONVerifier
	provider      AuthChainProvider
	stateProvider StateProvider
	// Set to true to do:
	// 6. Passes authorization rules based on the current state of the room, otherwise it is "soft failed".
	// This is only desirable for live events, not backfilled events hence the flag.
	performSoftFailCheck bool
}

// NewEventsLoader returns a new events loader
func NewEventsLoader(roomVer RoomVersion, keyRing JSONVerifier, stateProvider StateProvider, provider AuthChainProvider, performSoftFailCheck bool) *EventsLoader {
	return &EventsLoader{
		roomVer:              roomVer,
		keyRing:              keyRing,
		provider:             provider,
		stateProvider:        stateProvider,
		performSoftFailCheck: performSoftFailCheck,
	}
}

// LoadAndVerify loads untrusted events and verifies them.
// Checks performed are outlined at https://matrix.org/docs/spec/server_server/latest#checks-performed-on-receipt-of-a-pdu
// The length of the returned slice will always equal the length of rawEvents.
func (l *EventsLoader) LoadAndVerify(ctx context.Context, rawEvents []json.RawMessage) ([]EventLoadResult, error) {
	results := make([]EventLoadResult, len(rawEvents))

	// 1. Is a valid event, otherwise it is dropped.
	// 3. Passes hash checks, otherwise it is redacted before being processed further.
	events := make([]Event, len(rawEvents))
	for i, rawEv := range rawEvents {
		event, err := NewEventFromUntrustedJSON(rawEv, l.roomVer)
		if err != nil {
			results[i] = EventLoadResult{
				Error: err,
			}
			continue
		}
		// zero values are fine as VerifyEventSignatures will catch them, more important to keep the ordering
		events[i] = event
	}
	// 2. Passes signature checks, otherwise it is dropped.
	failures, err := VerifyEventSignatures(ctx, events, l.keyRing)
	if err != nil {
		return nil, err
	}
	if len(failures) != len(events) {
		return nil, fmt.Errorf("gomatrixserverlib: bulk event signature verification length mismatch: %d != %d", len(failures), len(events))
	}
	for i := range events {
		if eventErr := failures[i]; eventErr != nil {
			if results[i].Error == nil { // could have failed earlier
				results[i] = EventLoadResult{
					Error: eventErr,
				}
				continue
			}
		}
		h := events[i].Headered(l.roomVer)
		// 4. Passes authorization rules based on the event's auth events, otherwise it is rejected.
		if err := VerifyEventAuthChain(ctx, h, l.provider); err != nil {
			if results[i].Error == nil { // could have failed earlier
				results[i] = EventLoadResult{
					Error: err,
				}
				continue
			}
		}

		// 5. Passes authorization rules based on the state at the event, otherwise it is rejected.
		if err := VerifyAuthRulesAtState(ctx, l.stateProvider, h, h.EventID(), true); err != nil {
			if results[i].Error == nil { // could have failed earlier
				results[i] = EventLoadResult{
					Error: err,
				}
				continue
			}
		}
		results[i] = EventLoadResult{
			Event: &h,
		}
	}

	// TODO: performSoftFailCheck, needs forward extremity
	return results, nil
}