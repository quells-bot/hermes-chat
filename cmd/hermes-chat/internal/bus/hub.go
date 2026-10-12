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

// broadcastEdit offers e to the live subscriptions of the given humans, in
// memory; a subscription whose buffer is full drops the edit. It never blocks.
// The caller names the humans of the message's room: an edit carries message
// text, so nobody else may hear it. Agents get no edits.
func (h *Hub) broadcastEdit(e LiveEdit, humans []ulid.ULID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		if sub.edits == nil || !slices.Contains(humans, sub.actor.ID) {
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
	// again holds the newest delivery row of each of an agent's unacknowledged
	// deliveries at or behind its starting cursor; every agent stream is handed
	// them once more (delivery is at-least-once; the plugin dedupes by message id).
	again []storage.Event
}

// Edits receives live edits; nil for an agent's subscription.
func (s *Subscription) Edits() <-chan LiveEdit { return s.edits }

// Subscribe opens a stream of the events the actor may see, starting after the
// event with the given ID. FromNow, or an ID beyond the end of the log, starts
// at the end. Nothing committed after Subscribe returns is missed. Registering
// the stream first makes the agent count as connected, so the presence sweep
// that follows releases its waiting queue; the presence rows and the releases
// commit together, one ring. A second subscription for the same agent replaces
// the first: the old one's wake channel is closed, so its handler returns.
// An agent is also handed again its unacknowledged deliveries at or behind the
// cursor, whatever cursor it names: delivery is at-least-once.
func (s *service) Subscribe(ctx context.Context, actor Actor, after int64) (*Subscription, error) {
	if after < FromNow {
		return nil, ValidationError{Message: "cursor must not be negative"}
	}
	last, err := s.repo.GetLastEventID(ctx)
	if err != nil {
		return nil, fmt.Errorf("subscribe: get last event id: %w", err)
	}
	fromNow := after == FromNow
	if fromNow || after > last {
		after = last
	}
	// A delivery released to a stream that died before reading it, or skipped
	// while its room was paused, would hold its pair's queue for good if the
	// agent's cursor has moved past its row. So every agent subscribe hands
	// over again the deliveries still 'delivered' whose rows lie at or behind
	// the starting cursor (rows past it arrive by ordinary replay): the same
	// log rows, not a second release, one per message.
	var again []storage.Event
	if actor.Kind == ActorAgent {
		rows, err := s.repo.ListUnacknowledgedDeliveryEvents(ctx, actor.ID.String(), after)
		if err != nil {
			return nil, fmt.Errorf("subscribe: list unacknowledged deliveries: %w", err)
		}
		again = newestPerSubject(rows)
	}
	// The cursor was read before the presence sweep, so the events it commits
	// lie past it and the first Poll reads them.
	var edits chan LiveEdit // agents get no live edits
	if actor.Kind == ActorHuman {
		edits = make(chan LiveEdit, editsBuffer)
	}
	sub := &Subscription{svc: s, actor: actor, cursor: after, wake: make(chan struct{}, 1), edits: edits, again: again}
	if !s.hub.add(sub) {
		return nil, ErrUnavailable
	}
	if actor.Kind == ActorAgent {
		if err := s.record(ctx, func(r storage.Repository) error {
			roomIDs, err := r.ListAgentRoomIDs(ctx, actor.ID.String())
			if err != nil {
				return fmt.Errorf("list agent rooms: %w", err)
			}
			at := s.now()
			for _, roomID := range roomIDs {
				if err := putEvent(ctx, r, EventAgentPresence, roomID, actor.ID.String(), at); err != nil {
					return err
				}
			}
			return s.releaseSweep(ctx, r, actor.ID, at)
		}); err != nil {
			sub.Close()
			return nil, err
		}
	}
	return sub, nil
}

// newestPerSubject keeps the last row for each subject, in ascending order.
func newestPerSubject(rows []storage.Event) []storage.Event {
	newest := make(map[string]int64, len(rows))
	for _, row := range rows {
		newest[row.SubjectID] = row.ID
	}
	out := make([]storage.Event, 0, len(newest))
	for _, row := range rows {
		if newest[row.SubjectID] == row.ID {
			out = append(out, row)
		}
	}
	return out
}

// releaseSweep releases every releasable delivery head for the agent. Call it
// inside a record transaction; at stamps the events it writes.
func (s *service) releaseSweep(ctx context.Context, r storage.Repository, agentID ulid.ULID, at time.Time) error {
	rows, err := r.ReleasableDeliveries(ctx, agentID.String())
	if err != nil {
		return fmt.Errorf("list releasable: %w", err)
	}
	for _, d := range rows {
		ar, err := r.GetAgentByID(ctx, d.AgentID)
		if err != nil {
			return fmt.Errorf("get agent: %w", err)
		}
		agent, err := agentFromRow(ar)
		if err != nil {
			return err
		}
		if err := s.releaseHead(ctx, r, d.RoomID, agent, at, true); err != nil {
			return err
		}
	}
	return nil
}

// AgentGone records that the agent's stream closed: if it was the agent's
// last stream, one agent.presence event per room, connected=false. Called by
// the stream handler after Subscription.Close. A human actor is refused.
func (s *service) AgentGone(ctx context.Context, actor Actor) error {
	if actor.Kind != ActorAgent {
		return ErrForbidden
	}
	at := s.now()
	err := s.record(ctx, func(r storage.Repository) error {
		// Only the last stream's close flips presence off: a replacement is
		// already counted as connected, so writing more rows would lie.
		if s.hub.agentConnected(actor.ID) {
			return nil
		}
		roomIDs, err := r.ListAgentRoomIDs(ctx, actor.ID.String())
		if err != nil {
			return fmt.Errorf("list agent rooms: %w", err)
		}
		for _, roomID := range roomIDs {
			if err := putEvent(ctx, r, EventAgentPresence, roomID, actor.ID.String(), at); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("agent gone: %w", err)
	}
	return nil
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
	if len(s.again) > 0 {
		again := s.again
		s.again = nil
		events := make([]Event, 0, len(again))
		for _, row := range again {
			e, ok, err := s.event(ctx, repo, row)
			if err != nil {
				s.again = again
				return nil, fmt.Errorf("poll: event %d: %w", row.ID, err)
			}
			if ok {
				e.ID = s.cursor // an old row: it must not move the reader's cursor back
				events = append(events, e)
			}
		}
		if len(events) > 0 {
			return events, nil
		}
	}
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
	case EventMessagePosted, EventMessageEdited, EventDeliveryChanged, EventPromptChanged:
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
	case EventActivityPosted:
		act, err := r.GetActivity(ctx, row.SubjectID)
		if errors.Is(err, storage.ErrNotFound) {
			return Event{}, false, nil
		}
		if err != nil {
			return Event{}, false, fmt.Errorf("get activity: %w", err)
		}
		a, err := activityFromRow(act)
		if err != nil {
			return Event{}, false, err
		}
		e.Activity = &a // the activity carries its turn's message id
	case EventControlStop, EventControlPause:
		// There is nothing to load: the event's subject is the room itself,
		// already set on e.RoomID.
	case EventControlSteer:
		// A closed room hands an agent no human text, even a steer written
		// before the pause that the agent missed while its stream was down.
		// The skipped steer is dropped: resuming the room does not send it.
		roomRow, err := r.GetRoom(ctx, row.RoomID)
		if errors.Is(err, storage.ErrNotFound) {
			return Event{}, false, nil
		}
		if err != nil {
			return Event{}, false, fmt.Errorf("get room: %w", err)
		}
		if closed, err := paused(ctx, r, roomRow); err != nil || closed {
			return Event{}, false, err
		}
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
	case EventPromptAnswered:
		// An answer the agent has not read when its room closes is dropped,
		// exactly as a steer is: the pause has already told the plugin to stop
		// that turn, and resuming the room does not send the answer.
		roomRow, err := r.GetRoom(ctx, row.RoomID)
		if errors.Is(err, storage.ErrNotFound) {
			return Event{}, false, nil
		}
		if err != nil {
			return Event{}, false, fmt.Errorf("get room: %w", err)
		}
		if closed, err := paused(ctx, r, roomRow); err != nil || closed {
			return Event{}, false, err
		}
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
		// an expired prompt hands over nothing; nor does one answered twice
		if msgs[0].Prompt == nil || msgs[0].Prompt.State != PromptAnswered {
			return Event{}, false, nil
		}
		e.Message = &msgs[0]
	case EventAgentPresence:
		// The subject is the agent whose connection changed; show whether it
		// is connected now. A deleted agent skips the row.
		ar, err := r.GetAgentByID(ctx, row.SubjectID)
		if errors.Is(err, storage.ErrNotFound) {
			return Event{}, false, nil
		}
		if err != nil {
			return Event{}, false, fmt.Errorf("get agent: %w", err)
		}
		agent, err := agentFromRow(ar)
		if err != nil {
			return Event{}, false, err
		}
		e.Agent = &agent
		e.Connected = s.svc.hub.agentConnected(agent.ID)
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
		if err := loadReplyTo(ctx, r, e.Message); err != nil {
			return Event{}, false, err
		}
		roomRow, err := r.GetRoom(ctx, row.RoomID)
		if errors.Is(err, storage.ErrNotFound) {
			return Event{}, false, nil
		}
		if err != nil {
			return Event{}, false, fmt.Errorf("get room: %w", err)
		}
		// A closed room hands nothing over, not even a delivery already in the
		// log; resuming the room hands it over again.
		if closed, err := paused(ctx, r, roomRow); err != nil || closed {
			return Event{}, false, err
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

// loadReplyTo sets m.ReplyTo to the message m replies to, when it has one. A
// reply-to that is gone (its message row was deleted) loads as nil.
func loadReplyTo(ctx context.Context, r storage.Repository, m *Message) error {
	if m.ReplyToID.IsZero() {
		return nil
	}
	row, err := r.GetMessage(ctx, m.ReplyToID.String())
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get reply-to: %w", err)
	}
	msgs, err := messagesFromRows(ctx, r, []storage.Message{row})
	if err != nil {
		return err
	}
	m.ReplyTo = &msgs[0]
	return nil
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
