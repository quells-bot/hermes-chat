package bus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// FromNow, as Subscribe's cursor, starts a stream at the end of the log.
const FromNow int64 = -1

// pollBatch is the most log rows one read of a Poll takes.
const pollBatch = 100

// Hub holds the live subscriptions. It is a grace.Runner: when Run's context
// ends it closes them all.
//
// The hub is a doorbell, not a queue: it carries no events and knows nothing
// about rooms. Every subscription is rung for every committed change, and
// each reads the event log from its own cursor to find out what it may see.
type Hub struct {
	mu      sync.Mutex
	subs    map[*Subscription]struct{} // guarded by mu
	stopped bool                       // guarded by mu
}

func newHub() *Hub {
	return &Hub{subs: map[*Subscription]struct{}{}}
}

// Run blocks until ctx is done, then stops the hub: every subscription's wake
// channel is closed and no more are accepted. It always returns nil. The hub
// works without Run; Run adds only the shutdown.
func (h *Hub) Run(ctx context.Context) error {
	<-ctx.Done()
	h.stop()
	return nil
}

// stop closes every wake channel and empties the set. Wake channels are closed
// nowhere else, and a closed one is no longer in the set, so none is closed
// twice and ring never sends on a closed one.
func (h *Hub) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopped = true
	for sub := range h.subs {
		close(sub.wake)
	}
	clear(h.subs)
}

// ring wakes every subscription. A wake channel holds one pending ring, so a
// ring is never lost, never queued twice, and never waits for a subscriber.
func (h *Hub) ring() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

// add registers sub. It reports false if the hub has stopped.
func (h *Hub) add(sub *Subscription) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return false
	}
	h.subs[sub] = struct{}{}
	return true
}

func (h *Hub) remove(sub *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, sub)
}

func (h *Hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Hub returns the service's hub, for the caller that runs it.
func (s *service) Hub() *Hub { return s.hub }

// Subscription is one stream. It is not safe for use by more than one goroutine.
//
// Use it like this: call Poll until it returns nothing, wait on Wake, repeat.
type Subscription struct {
	svc    *service
	actor  Actor
	cursor int64
	wake   chan struct{} // sent to and closed by the hub alone
}

// Subscribe opens a stream of the events the actor may see, starting after the
// event with the given ID. FromNow, or an ID beyond the end of the log, starts
// at the end. Nothing committed after Subscribe returns is missed.
func (s *service) Subscribe(ctx context.Context, actor Actor, after int64) (*Subscription, error) {
	if actor.Kind != ActorHuman {
		return nil, ErrForbidden
	}
	if after < FromNow {
		return nil, ValidationError{Message: "cursor must not be negative"}
	}
	last, err := s.repo.GetLastEventID(ctx)
	if err != nil {
		return nil, fmt.Errorf("subscribe: get last event id: %w", err)
	}
	if after == FromNow || after > last {
		after = last
	}
	// The cursor was read before registering. An event committed in between
	// rings nobody here, but it lies past the cursor, so the first Poll reads it.
	sub := &Subscription{svc: s, actor: actor, cursor: after, wake: make(chan struct{}, 1)}
	if !s.hub.add(sub) {
		return nil, ErrUnavailable
	}
	return sub, nil
}

// Cursor returns the ID of the last event read, or where the stream started.
func (s *Subscription) Cursor() int64 { return s.cursor }

// Wake receives when Poll may have something. It is closed when the hub stops.
func (s *Subscription) Wake() <-chan struct{} { return s.wake }

// Close takes the subscription out of the hub. It is safe to call twice, and
// after the hub has stopped. It does not close the wake channel.
func (s *Subscription) Close() { s.svc.hub.remove(s) }

// Poll returns the next events the subscriber may see, at most 100, and moves
// the cursor past the log rows it read. An empty result means the log is
// drained. On an error the cursor stays where it was.
func (s *Subscription) Poll(ctx context.Context) ([]Event, error) {
	repo := s.svc.repo
	for {
		rows, err := repo.ListEventsForHuman(ctx, s.actor.ID.String(), s.cursor, pollBatch)
		if err != nil {
			return nil, fmt.Errorf("poll: list events: %w", err)
		}
		events := make([]Event, 0, len(rows))
		for _, row := range rows {
			e, ok, err := s.event(ctx, repo, row)
			if err != nil {
				return nil, fmt.Errorf("poll: event %d: %w", row.ID, err)
			}
			if ok {
				events = append(events, e)
			}
		}
		if len(rows) > 0 {
			s.cursor = rows[len(rows)-1].ID
		}
		// A full batch that produced nothing says nothing about what follows it.
		if len(events) > 0 || len(rows) < pollBatch {
			return events, nil
		}
	}
}

// event turns a log row into an Event for this subscriber. ok is false when
// the row's subject can no longer be loaded, and the row is to be skipped.
func (s *Subscription) event(ctx context.Context, r storage.Repository, row storage.Event) (e Event, ok bool, err error) {
	roomID, err := ulid.ParseStrict(row.RoomID)
	if err != nil {
		return Event{}, false, fmt.Errorf("stored event has an invalid room id %q: %w", row.RoomID, err)
	}
	e = Event{ID: row.ID, Kind: EventKind(row.Kind), RoomID: roomID, At: time.UnixMilli(row.CreatedAtMs).UTC()}
	switch e.Kind {
	case EventRoomChanged:
		roomRow, err := r.GetRoom(ctx, row.SubjectID)
		if errors.Is(err, storage.ErrNotFound) {
			return Event{}, false, nil
		}
		if err != nil {
			return Event{}, false, fmt.Errorf("get room: %w", err)
		}
		room, err := loadRoom(ctx, r, roomRow, s.actor.ID)
		if errors.Is(err, ErrNotFound) { // the subscriber left the room since the row was listed
			return Event{}, false, nil
		}
		if err != nil {
			return Event{}, false, err
		}
		e.Room = &room
	case EventMessagePosted:
		msgRow, err := r.GetMessage(ctx, row.SubjectID)
		if errors.Is(err, storage.ErrNotFound) {
			return Event{}, false, nil
		}
		if err != nil {
			return Event{}, false, fmt.Errorf("get message: %w", err)
		}
		msgs, err := messagesFromRows(ctx, r, []storage.Message{msgRow})
		if err != nil {
			return Event{}, false, err
		}
		e.Message = &msgs[0]
	default:
		return Event{}, false, fmt.Errorf("unknown kind %q", row.Kind)
	}
	return e, true, nil
}
