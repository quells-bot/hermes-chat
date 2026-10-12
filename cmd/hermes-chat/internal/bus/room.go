package bus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// ErrRoomArchived reports a change to an archived room.
var ErrRoomArchived = errors.New("room is archived")

// RoomFlavor says what kind of conversation a room is.
type RoomFlavor string

const (
	FlavorDM           RoomFlavor = "dm"           // one human and one agent, no topic
	FlavorConversation RoomFlavor = "conversation" // a topic room with one agent
	FlavorChannel      RoomFlavor = "channel"      // a topic room with several agents
)

// Room is a room as one human sees it.
type Room struct {
	ID                ulid.ULID
	Flavor            RoomFlavor
	Topic             string      // "" for a DM
	CreatedAt         time.Time   // UTC, millisecond precision
	ArchivedAt        time.Time   // zero while live
	Humans            []Human     // ordered by username
	Agents            []RoomAgent // ordered by handle
	LastMessageID     ulid.ULID   // zero when the room has no messages
	LastReadMessageID ulid.ULID   // the viewer's read marker; zero when nothing is read
}

// Archived reports whether the room is archived.
func (r Room) Archived() bool { return !r.ArchivedAt.IsZero() }

// RoomUpdate changes what is non-nil. Archived is applied before Topic.
type RoomUpdate struct {
	Topic    *string
	Archived *bool
}

func validateTopic(topic string) (string, error) {
	topic = strings.TrimSpace(topic)
	n := utf8.RuneCountInString(topic)
	if n < 1 || n > 200 || strings.IndexFunc(topic, unicode.IsControl) >= 0 {
		return "", ValidationError{Message: "topic must be 1-200 characters with no control characters"}
	}
	return topic, nil
}

var (
	errNoSuchAgent  = ValidationError{Message: "no such agent"}
	errNoSuchUser   = ValidationError{Message: "no such user"}
	errNeedAgent    = ValidationError{Message: "a room needs at least one agent"}
	errDMUnchanging = ValidationError{Message: "a DM cannot be changed"}
)

// wrapOp adds the operation to an unexpected error. A ValidationError passes
// through, because its text is the message shown to the caller.
func wrapOp(op string, err error) error {
	var ve ValidationError
	if errors.As(err, &ve) {
		return ve
	}
	return fmt.Errorf("%s: %w", op, err)
}

func parseOptionalID(s string) (ulid.ULID, error) {
	if s == "" {
		return ulid.ULID{}, nil
	}
	id, err := ulid.ParseStrict(s)
	if err != nil {
		return ulid.ULID{}, fmt.Errorf("stored id %q is invalid: %w", s, err)
	}
	return id, nil
}

func roomFlavor(row storage.Room, agents int) RoomFlavor {
	switch {
	case row.DMHumanID != nil:
		return FlavorDM
	case agents > 1:
		return FlavorChannel
	default:
		return FlavorConversation
	}
}

// roomForMember returns the room if actor is a human member of it: ErrForbidden
// for any other kind of actor, ErrNotFound otherwise.
func roomForMember(ctx context.Context, r storage.Repository, actor Actor, roomID ulid.ULID) (storage.Room, error) {
	if actor.Kind != ActorHuman {
		return storage.Room{}, ErrForbidden
	}
	_, err := r.GetRoomHuman(ctx, roomID.String(), actor.ID.String())
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Room{}, fmt.Errorf("room %s: %w", roomID, ErrNotFound)
	}
	if err != nil {
		return storage.Room{}, fmt.Errorf("get room member: %w", err)
	}
	row, err := r.GetRoom(ctx, roomID.String())
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Room{}, fmt.Errorf("room %s: %w", roomID, ErrNotFound)
	}
	if err != nil {
		return storage.Room{}, fmt.Errorf("get room: %w", err)
	}
	return row, nil
}

// buildRoomLayout fills the room's members, flavor, and newest message. The
// viewer, when it is a human in the room, fills the read marker; loadRoomForAgent
// leaves it zero.
func (s *service) buildRoomLayout(ctx context.Context, r storage.Repository, row storage.Room) (Room, error) {
	id, err := ulid.ParseStrict(row.ID)
	if err != nil {
		return Room{}, fmt.Errorf("stored room has an invalid id %q: %w", row.ID, err)
	}
	humanRows, err := r.ListRoomHumans(ctx, row.ID)
	if err != nil {
		return Room{}, fmt.Errorf("list room humans: %w", err)
	}
	agentRows, err := r.ListRoomAgents(ctx, row.ID)
	if err != nil {
		return Room{}, fmt.Errorf("list room agents: %w", err)
	}
	room := Room{
		ID: id, Flavor: roomFlavor(row, len(agentRows)), Topic: row.Topic,
		CreatedAt: time.UnixMilli(row.CreatedAtMs).UTC(),
		Humans:    make([]Human, 0, len(humanRows)), Agents: make([]RoomAgent, 0, len(agentRows)),
	}
	if row.ArchivedAtMs != nil {
		room.ArchivedAt = time.UnixMilli(*row.ArchivedAtMs).UTC()
	}
	for _, hr := range humanRows {
		h, err := humanFromRow(hr)
		if err != nil {
			return Room{}, err
		}
		room.Humans = append(room.Humans, h)
	}
	for _, ar := range agentRows {
		a, err := agentFromRow(ar)
		if err != nil {
			return Room{}, err
		}
		room.Agents = append(room.Agents, RoomAgent{Agent: a, Connected: s.hub.agentConnected(a.ID)})
	}
	last, err := r.GetLastMessageID(ctx, row.ID)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return Room{}, fmt.Errorf("get last message: %w", err)
	}
	if room.LastMessageID, err = parseOptionalID(last); err != nil {
		return Room{}, err
	}
	return room, nil
}

// loadRoom builds the Room the human viewer sees: members, flavor, the newest
// message, the viewer's read marker.
func (s *service) loadRoom(ctx context.Context, r storage.Repository, row storage.Room, viewer ulid.ULID) (Room, error) {
	room, err := s.buildRoomLayout(ctx, r, row)
	if err != nil {
		return Room{}, err
	}
	member, err := r.GetRoomHuman(ctx, row.ID, viewer.String())
	if errors.Is(err, storage.ErrNotFound) {
		return Room{}, fmt.Errorf("room %s: %w", row.ID, ErrNotFound)
	}
	if err != nil {
		return Room{}, fmt.Errorf("get room member: %w", err)
	}
	if room.LastReadMessageID, err = parseOptionalID(member.LastReadMessageID); err != nil {
		return Room{}, err
	}
	return room, nil
}

// loadRoomForAgent builds the Room as an agent sees it: the same members and
// flavor, connection flags filled, and no read marker. It does not require a
// human viewer.
func (s *service) loadRoomForAgent(ctx context.Context, r storage.Repository, row storage.Room) (Room, error) {
	return s.buildRoomLayout(ctx, r, row)
}

// record runs fn in one write transaction and, once it has committed, rings
// the hub. It is for the changes that write an event; nothing else rings.
func (s *service) record(ctx context.Context, fn func(storage.Repository) error) error {
	if err := s.repo.InTx(ctx, fn); err != nil {
		return err
	}
	s.hub.ring()
	return nil
}

// requireAgents checks that every id names an agent, once each.
func requireAgents(ctx context.Context, r storage.Repository, ids []ulid.ULID) ([]ulid.ULID, error) {
	seen := make(map[ulid.ULID]bool, len(ids))
	out := make([]ulid.ULID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := requireAgent(ctx, r, id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func requireAgent(ctx context.Context, r storage.Repository, id ulid.ULID) error {
	_, err := r.GetAgentByID(ctx, id.String())
	if errors.Is(err, storage.ErrNotFound) {
		return errNoSuchAgent
	}
	if err != nil {
		return fmt.Errorf("get agent: %w", err)
	}
	return nil
}

// CreateRoom makes a topic room with the actor as its only human.
func (s *service) CreateRoom(ctx context.Context, actor Actor, topic string, agentIDs []ulid.ULID) (Room, error) {
	if actor.Kind != ActorHuman {
		return Room{}, ErrForbidden
	}
	topic, err := validateTopic(topic)
	if err != nil {
		return Room{}, err
	}
	at := s.now()
	id, err := newID(at)
	if err != nil {
		return Room{}, fmt.Errorf("generate id: %w", err)
	}
	err = s.record(ctx, func(r storage.Repository) error {
		agents, err := requireAgents(ctx, r, agentIDs)
		if err != nil {
			return err
		}
		if len(agents) == 0 {
			return errNeedAgent
		}
		if err := r.PutRoom(ctx, storage.Room{ID: id.String(), Topic: topic, CreatedAtMs: at.UnixMilli()}); err != nil {
			return fmt.Errorf("put room: %w", err)
		}
		if err := r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: id.String(), HumanID: actor.ID.String(), JoinedAtMs: at.UnixMilli()}); err != nil {
			return fmt.Errorf("put room human: %w", err)
		}
		for _, a := range agents {
			if err := r.PutRoomAgent(ctx, storage.RoomAgent{RoomID: id.String(), AgentID: a.String(), JoinedAtMs: at.UnixMilli()}); err != nil {
				return fmt.Errorf("put room agent: %w", err)
			}
		}
		return putEvent(ctx, r, EventRoomChanged, id.String(), id.String(), at)
	})
	if err != nil {
		return Room{}, wrapOp("create room", err)
	}
	return s.GetRoom(ctx, actor, id)
}

// OpenDM returns the actor's DM with an agent, making it if there is none.
func (s *service) OpenDM(ctx context.Context, actor Actor, agentID ulid.ULID) (Room, bool, error) {
	if actor.Kind != ActorHuman {
		return Room{}, false, ErrForbidden
	}
	at := s.now()
	newRoomID, err := newID(at)
	if err != nil {
		return Room{}, false, fmt.Errorf("generate id: %w", err)
	}
	roomID := newRoomID
	created := false
	err = s.record(ctx, func(r storage.Repository) error {
		if err := requireAgent(ctx, r, agentID); err != nil {
			return err
		}
		existing, err := r.GetDMRoom(ctx, actor.ID.String(), agentID.String())
		if err == nil {
			if roomID, err = ulid.ParseStrict(existing.ID); err != nil {
				return fmt.Errorf("stored room has an invalid id %q: %w", existing.ID, err)
			}
			return nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("get dm room: %w", err)
		}
		hid, aid, rid := actor.ID.String(), agentID.String(), roomID.String()
		if err := r.PutRoom(ctx, storage.Room{ID: rid, DMHumanID: &hid, DMAgentID: &aid, CreatedAtMs: at.UnixMilli()}); err != nil {
			return fmt.Errorf("put room: %w", err)
		}
		if err := r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: rid, HumanID: hid, JoinedAtMs: at.UnixMilli()}); err != nil {
			return fmt.Errorf("put room human: %w", err)
		}
		if err := r.PutRoomAgent(ctx, storage.RoomAgent{RoomID: rid, AgentID: aid, JoinedAtMs: at.UnixMilli()}); err != nil {
			return fmt.Errorf("put room agent: %w", err)
		}
		created = true
		return putEvent(ctx, r, EventRoomChanged, rid, rid, at)
	})
	if err != nil {
		return Room{}, false, wrapOp("open dm", err)
	}
	room, err := s.GetRoom(ctx, actor, roomID)
	return room, created, err
}

// ListRooms returns the actor's rooms ordered by id, archived ones included.
// A human sees read markers; an agent sees connection flags and no markers. A
// kind that cannot be a room member is ErrForbidden.
func (s *service) ListRooms(ctx context.Context, actor Actor) ([]Room, error) {
	switch actor.Kind {
	case ActorHuman:
		rows, err := s.repo.ListRoomsByHuman(ctx, actor.ID.String())
		if err != nil {
			return nil, fmt.Errorf("list rooms: %w", err)
		}
		out := make([]Room, 0, len(rows))
		for _, row := range rows {
			room, err := s.loadRoom(ctx, s.repo, row, actor.ID)
			if err != nil {
				return nil, err
			}
			out = append(out, room)
		}
		return out, nil
	case ActorAgent:
		ids, err := s.repo.ListAgentRoomIDs(ctx, actor.ID.String())
		if err != nil {
			return nil, fmt.Errorf("list rooms: %w", err)
		}
		out := make([]Room, 0, len(ids))
		for _, id := range ids {
			row, err := s.repo.GetRoom(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("get room: %w", err)
			}
			room, err := s.loadRoomForAgent(ctx, s.repo, row)
			if err != nil {
				return nil, err
			}
			out = append(out, room)
		}
		return out, nil
	default:
		return nil, ErrForbidden
	}
}

// GetRoom returns a room the actor is a member of.
func (s *service) GetRoom(ctx context.Context, actor Actor, roomID ulid.ULID) (Room, error) {
	row, err := roomForMember(ctx, s.repo, actor, roomID)
	if err != nil {
		return Room{}, err
	}
	return s.loadRoom(ctx, s.repo, row, actor.ID)
}

// mutateRoom runs a change to a topic room: membership, then DM, then
// archived (unless the change is allowed on an archived room), then fn. fn
// reports whether it changed anything; a real change writes one room.changed
// event. It returns the room as the actor sees it after the commit.
func (s *service) mutateRoom(ctx context.Context, op string, actor Actor, roomID ulid.ULID, allowArchived bool, fn func(r storage.Repository, row storage.Room, at time.Time) (changed bool, err error)) (Room, error) {
	at := s.now()
	err := s.record(ctx, func(r storage.Repository) error {
		row, err := roomForMember(ctx, r, actor, roomID)
		if err != nil {
			return err
		}
		if row.DMHumanID != nil {
			return errDMUnchanging
		}
		if row.ArchivedAtMs != nil && !allowArchived {
			return ErrRoomArchived
		}
		changed, err := fn(r, row, at)
		if err != nil || !changed {
			return err
		}
		return putEvent(ctx, r, EventRoomChanged, row.ID, row.ID, at)
	})
	if err != nil {
		return Room{}, wrapOp(op, err)
	}
	return s.GetRoom(ctx, actor, roomID)
}

// UpdateRoom applies the non-nil fields of u: Archived first, then Topic.
func (s *service) UpdateRoom(ctx context.Context, actor Actor, roomID ulid.ULID, u RoomUpdate) (Room, error) {
	return s.mutateRoom(ctx, "update room", actor, roomID, true, func(r storage.Repository, row storage.Room, at time.Time) (bool, error) {
		archived := row.ArchivedAtMs != nil
		changed := false
		if u.Archived != nil && *u.Archived != archived {
			var ms *int64
			if *u.Archived {
				v := at.UnixMilli()
				ms = &v
			}
			if err := r.SetRoomArchivedAt(ctx, row.ID, ms); err != nil {
				return false, fmt.Errorf("set archived: %w", err)
			}
			archived = *u.Archived
			changed = true
			if !*u.Archived {
				// the room is live again, so the queue thaws to connected agents
				if err := s.thawQueue(ctx, r, row, at); err != nil {
					return false, err
				}
			}
		}
		if u.Topic == nil {
			return changed, nil
		}
		if archived {
			return false, ErrRoomArchived
		}
		topic, err := validateTopic(*u.Topic)
		if err != nil {
			return false, err
		}
		if topic == row.Topic {
			return changed, nil
		}
		if err := r.SetRoomTopic(ctx, row.ID, topic); err != nil {
			return false, fmt.Errorf("set topic: %w", err)
		}
		return true, nil
	})
}

// InviteHuman makes another human a member of the room.
func (s *service) InviteHuman(ctx context.Context, actor Actor, roomID, humanID ulid.ULID) (Room, error) {
	return s.mutateRoom(ctx, "invite human", actor, roomID, false, func(r storage.Repository, row storage.Room, at time.Time) (bool, error) {
		if _, err := r.GetHumanByID(ctx, humanID.String()); errors.Is(err, storage.ErrNotFound) {
			return false, errNoSuchUser
		} else if err != nil {
			return false, fmt.Errorf("get human: %w", err)
		}
		if _, err := r.GetRoomHuman(ctx, row.ID, humanID.String()); err == nil {
			return false, nil
		} else if !errors.Is(err, storage.ErrNotFound) {
			return false, fmt.Errorf("get room member: %w", err)
		}
		if err := r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: row.ID, HumanID: humanID.String(), JoinedAtMs: at.UnixMilli()}); err != nil {
			return false, fmt.Errorf("put room human: %w", err)
		}
		return true, nil
	})
}

// AddRoomAgent adds an agent to the room.
func (s *service) AddRoomAgent(ctx context.Context, actor Actor, roomID, agentID ulid.ULID) (Room, error) {
	return s.mutateRoom(ctx, "add room agent", actor, roomID, false, func(r storage.Repository, row storage.Room, at time.Time) (bool, error) {
		if err := requireAgent(ctx, r, agentID); err != nil {
			return false, err
		}
		err := r.PutRoomAgent(ctx, storage.RoomAgent{RoomID: row.ID, AgentID: agentID.String(), JoinedAtMs: at.UnixMilli()})
		if errors.Is(err, storage.ErrConflict) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("put room agent: %w", err)
		}
		return true, nil
	})
}

// RemoveRoomAgent takes an agent out of the room, which must keep at least one.
func (s *service) RemoveRoomAgent(ctx context.Context, actor Actor, roomID, agentID ulid.ULID) (Room, error) {
	return s.mutateRoom(ctx, "remove room agent", actor, roomID, false, func(r storage.Repository, row storage.Room, at time.Time) (bool, error) {
		agents, err := r.ListRoomAgents(ctx, row.ID)
		if err != nil {
			return false, fmt.Errorf("list room agents: %w", err)
		}
		present := false
		for _, a := range agents {
			present = present || a.ID == agentID.String()
		}
		if !present {
			return false, nil
		}
		if len(agents) == 1 {
			return false, errNeedAgent
		}
		if err := r.DeleteRoomAgent(ctx, row.ID, agentID.String()); err != nil {
			return false, fmt.Errorf("delete room agent: %w", err)
		}
		// membership changes ride mutateRoom; the removed agent's waiting
		// deliveries are cancelled so they can never reach it.
		if _, err := r.CancelDeliveries(ctx, row.ID, agentID.String()); err != nil {
			return false, fmt.Errorf("cancel deliveries: %w", err)
		}
		return true, nil
	})
}

// thawQueue releases the current head to every room agent after the room is
// live again: an unarchived room's queue thaws to its connected agents.
func (s *service) thawQueue(ctx context.Context, r storage.Repository, row storage.Room, at time.Time) error {
	live := row
	live.ArchivedAtMs = nil
	agentRows, err := r.ListRoomAgents(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list room agents: %w", err)
	}
	for _, ar := range agentRows {
		a, err := agentFromRow(ar)
		if err != nil {
			return err
		}
		if err := s.releaseHead(ctx, r, live, a, at, true); err != nil {
			return err
		}
	}
	return nil
}
