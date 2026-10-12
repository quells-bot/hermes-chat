package bus

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// FromNow, as Subscribe's cursor, starts a stream at the end of the log.
const FromNow int64 = -1

// pollBatch is the most log rows one read of a Poll takes.
const pollBatch = 100

// editsBuffer bounds the edits channel of one human subscription.
const editsBuffer = 32

// LiveEdit is one streaming edit, pushed to live human streams from memory.
type LiveEdit struct {
	MessageID ulid.ULID
	Text      string
}

// Hub holds the live subscriptions. It is a grace.Runner: when Run's context
// ends it closes them all.
//
// The hub is a doorbell, not a queue: it carries no events and knows nothing
// about rooms. Every subscription is rung for every committed change, and
// each reads the event log from its own cursor to find out what it may see.
type Hub struct {
	mu      sync.Mutex
	subs    map[*Subscription]struct{}  // guarded by mu
	agents  map[ulid.ULID]*Subscription // guarded by mu
	stopped bool                        // guarded by mu
}

func newHub() *Hub {
	return &Hub{subs: map[*Subscription]struct{}{}, agents: map[ulid.ULID]*Subscription{}}
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
	clear(h.agents)
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

// add registers sub. It reports false if the hub has stopped. For an agent it
// first replaces any live stream of the same agent: the old one leaves subs and
// its wake channel is closed, so its handler returns.
func (h *Hub) add(sub *Subscription) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return false
	}
	if sub.actor.Kind == ActorAgent {
		if old, ok := h.agents[sub.actor.ID]; ok {
			delete(h.subs, old)
			close(old.wake)
		}
		h.agents[sub.actor.ID] = sub
	}
	h.subs[sub] = struct{}{}
	return true
}

func (h *Hub) remove(sub *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, sub)
	if sub.actor.Kind == ActorAgent && h.agents[sub.actor.ID] == sub {
		delete(h.agents, sub.actor.ID)
	}
}

func (h *Hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// agentConnected reports whether the agent has a live stream.
func (h *Hub) agentConnected(id ulid.ULID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.agents[id]
	return ok
}

// broadcastEdit offers e to every human subscription, in memory; a subscription
// whose buffer is full drops the edit. It never blocks. Agents get no edits.
func (h *Hub) broadcastEdit(e LiveEdit) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		if sub.edits == nil {
			continue
		}
		select {
		case sub.edits <- e:
		default:
		}
	}
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
	edits  chan LiveEdit // buffered live edits; nil for an agent's subscription
}

// Edits receives live edits; nil for an agent's subscription.
func (s *Subscription) Edits() <-chan LiveEdit { return s.edits }

// Subscribe opens a stream of the events the actor may see, starting after the
// event with the given ID. FromNow, or an ID beyond the end of the log, starts
// at the end. Nothing committed after Subscribe returns is missed. A second
// subscription for the same agent replaces the first: the old one's wake
// channel is closed, so its handler returns.
func (s *service) Subscribe(ctx context.Context, actor Actor, after int64) (*Subscription, error) {
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
	var edits chan LiveEdit // agents get no live edits
	if actor.Kind == ActorHuman {
		edits = make(chan LiveEdit, editsBuffer)
	}
	sub := &Subscription{svc: s, actor: actor, cursor: after, wake: make(chan struct{}, 1), edits: edits}
	if !s.hub.add(sub) {
		return nil, ErrUnavailable
	}
	return sub, nil
}

// Cursor returns the ID of the last event read, or where the stream started.
func (s *Subscription) Cursor() int64 { return s.cursor }

// Wake receives when Poll may have something. It is closed when the hub stops,
// or when an agent's stream is replaced by a newer one.
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
		var rows []storage.Event
		var err error
		if s.actor.Kind == ActorAgent {
			rows, err = repo.ListEventsForAgent(ctx, s.actor.ID.String(), s.cursor, pollBatch)
		} else {
			rows, err = repo.ListEventsForHuman(ctx, s.actor.ID.String(), s.cursor, pollBatch)
		}
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
		room, err := s.svc.loadRoom(ctx, r, roomRow, s.actor.ID)
		if errors.Is(err, ErrNotFound) { // the subscriber left the room since the row was listed
			return Event{}, false, nil
		}
		if err != nil {
			return Event{}, false, err
		}
		e.Room = &room
	case EventMessagePosted, EventDeliveryChanged:
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
	case EventDelivery:
		// Only an agent sees these (they are agent-addressed), so the room is
		// the delivered message's, shown as the agent sees it.
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
		roomRow, err := r.GetRoom(ctx, row.RoomID)
		if errors.Is(err, storage.ErrNotFound) {
			return Event{}, false, nil
		}
		if err != nil {
			return Event{}, false, fmt.Errorf("get room: %w", err)
		}
		room, err := s.svc.loadRoomForAgent(ctx, r, roomRow)
		if err != nil {
			return Event{}, false, err
		}
		e.Room = &room
		if room.Flavor == FlavorChannel {
			// the delivered message's predecessors, bounded in count and text
			ctxRows, err := r.ListMessages(ctx, row.RoomID, row.SubjectID, contextMessages)
			if err != nil {
				return Event{}, false, fmt.Errorf("list context: %w", err)
			}
			cmsgs, err := messagesFromRows(ctx, r, ctxRows)
			if err != nil {
				return Event{}, false, err
			}
			e.Context = trimContext(cmsgs, contextTextBytes)
		}
	default:
		return Event{}, false, fmt.Errorf("unknown kind %q", row.Kind)
	}
	return e, true, nil
}

// trimContext keeps the newest messages whose text fits within textBytes,
// dropped from the oldest end, and returns them oldest-first. msgs is
// newest-first.
func trimContext(msgs []Message, textBytes int) []Message {
	total := 0
	for i := range msgs {
		total += len(msgs[i].Text)
	}
	end := len(msgs)
	for end > 0 && total > textBytes {
		end--
		total -= len(msgs[end].Text)
	}
	kept := make([]Message, 0, end)
	kept = append(kept, msgs[:end]...)
	slices.Reverse(kept)
	return kept
}
