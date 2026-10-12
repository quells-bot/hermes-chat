package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"modernc.org/sqlite"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrNestedTx = errors.New("InTx called inside a transaction")
)

// SQLite extended result codes for unique and primary key violations.
const (
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintUnique     = 2067
)

// Repository is the storage surface the bus layer depends on.
type Repository interface {
	Ping(ctx context.Context) error
	// InTx runs fn in one write transaction. The Repository passed to fn
	// reads and writes through that transaction; calling InTx on it returns
	// ErrNestedTx. fn's error rolls back and is returned; a panic rolls back
	// and propagates. Inside fn, use only the Repository passed to it: calling a
	// write method on the outer repository waits forever, because the write pool
	// has one connection.
	InTx(ctx context.Context, fn func(Repository) error) error
	PutHuman(ctx context.Context, h Human) error                            // ErrConflict on duplicate id or username
	GetHumanByUsername(ctx context.Context, username string) (Human, error) // ErrNotFound
	ListHumans(ctx context.Context) ([]Human, error)                        // ordered by username
	GetHumanByID(ctx context.Context, id string) (Human, error)             // ErrNotFound
	SetHumanPasswordHash(ctx context.Context, id, hash string) error        // ErrNotFound
	DeleteHuman(ctx context.Context, id string) error                       // ErrNotFound; that human's sessions go with it

	PutAgent(ctx context.Context, a Agent) error                                // ErrConflict on duplicate id, handle, or token hash
	GetAgentByID(ctx context.Context, id string) (Agent, error)                 // ErrNotFound
	GetAgentByHandle(ctx context.Context, handle string) (Agent, error)         // ErrNotFound
	GetAgentByTokenHash(ctx context.Context, hash string) (Agent, error)        // ErrNotFound; revoked agents are returned, the bus decides
	ListAgents(ctx context.Context) ([]Agent, error)                            // ordered by handle; empty, not nil
	SetAgentTokenHash(ctx context.Context, id, hash string) error               // ErrNotFound; ErrConflict
	SetAgentRevokedAt(ctx context.Context, id string, revokedAtMs *int64) error // ErrNotFound; nil clears

	PutSession(ctx context.Context, s Session) error                         // ErrConflict on duplicate id or token hash
	GetSessionByTokenHash(ctx context.Context, hash string) (Session, error) // ErrNotFound; expired rows are returned, the bus decides
	DeleteSessionByTokenHash(ctx context.Context, hash string) error         // nil when there is no such row
	DeleteSessionsByHuman(ctx context.Context, humanID string) error
	DeleteExpiredSessions(ctx context.Context, nowMs int64) error // rows with expires_at <= nowMs

	PutRoom(ctx context.Context, r Room) error                                           // ErrConflict on duplicate id, or a second DM for the same human and agent
	GetRoom(ctx context.Context, id string) (Room, error)                                // ErrNotFound
	GetDMRoom(ctx context.Context, humanID, agentID string) (Room, error)                // ErrNotFound
	ListRoomsByHuman(ctx context.Context, humanID string) ([]Room, error)                // ordered by id; archived rooms included; empty, not nil
	SetRoomTopic(ctx context.Context, id, topic string) error                            // ErrNotFound
	SetRoomArchivedAt(ctx context.Context, id string, archivedAtMs *int64) error         // ErrNotFound; nil clears
	SetRoomPause(ctx context.Context, id string, pausedAtMs *int64, reason string) error // ErrNotFound; nil and "" clear
	SetRoomTurnCap(ctx context.Context, id string, turnCap int64) error                  // ErrNotFound
	SetRoomAgentTurns(ctx context.Context, id string, turns int64) error                 // ErrNotFound
	GetHaltedAtMs(ctx context.Context) (*int64, error)                                   // nil while not halted
	SetHaltedAtMs(ctx context.Context, haltedAtMs *int64) error                          // nil clears

	PutRoomHuman(ctx context.Context, m RoomHuman) error                               // ErrConflict if already a member
	GetRoomHuman(ctx context.Context, roomID, humanID string) (RoomHuman, error)       // ErrNotFound: not a member
	ListRoomHumans(ctx context.Context, roomID string) ([]Human, error)                // ordered by username; PasswordHash is left empty; empty, not nil
	SetRoomHumanLastRead(ctx context.Context, roomID, humanID, messageID string) error // ErrNotFound

	PutRoomAgent(ctx context.Context, m RoomAgent) error                // ErrConflict if already a member
	DeleteRoomAgent(ctx context.Context, roomID, agentID string) error  // ErrNotFound
	ListRoomAgents(ctx context.Context, roomID string) ([]Agent, error) // ordered by handle; empty, not nil
	ListAllRoomAgents(ctx context.Context) ([]RoomAgent, error)         // ordered by room id, then agent id; empty, not nil

	PutMessage(ctx context.Context, m Message) error                                           // ErrConflict on duplicate id
	GetMessage(ctx context.Context, id string) (Message, error)                                // ErrNotFound
	ListMessages(ctx context.Context, roomID, beforeID string, limit int64) ([]Message, error) // newest first; beforeID "" starts at the newest; empty, not nil
	GetLastMessageID(ctx context.Context, roomID string) (string, error)                       // ErrNotFound when the room has no messages

	SetMessageBody(ctx context.Context, id, body string) error                       // ErrNotFound
	FinalizeMessage(ctx context.Context, id, body string, finalizedAtMs int64) error // ErrNotFound; sets the body and marks the message finalized

	PutEvent(ctx context.Context, e Event) (int64, error)                                                   // e.ID is ignored; returns the id the log assigned
	ListEventsForHuman(ctx context.Context, humanID string, afterID, limit int64) ([]Event, error)          // ascending id; only rooms the human is a member of now, and only non-agent-targeted rows; empty, not nil
	ListEventsForAgent(ctx context.Context, agentID string, afterID, limit int64) ([]Event, error)          // ascending id; agent-targeted rows only; empty, not nil
	ListUnacknowledgedDeliveryEvents(ctx context.Context, agentID string, throughID int64) ([]Event, error) // ascending id; the delivery rows, up to throughID, of the agent's deliveries still in 'delivered'; empty, not nil
	GetLastEventID(ctx context.Context) (int64, error)                                                      // 0 when the log is empty

	PutDelivery(ctx context.Context, d Delivery) error                                             // ErrConflict on duplicate (room, agent, message)
	GetDelivery(ctx context.Context, agentID, messageID string) (Delivery, error)                  // ErrNotFound
	GetWorkingDelivery(ctx context.Context, roomID, agentID string) (Delivery, error)              // ErrNotFound: the pair's only working turn is its currently-working delivery
	GetDeliveredDelivery(ctx context.Context, roomID, agentID string) (Delivery, error)            // ErrNotFound: the pair has no delivery in 'delivered'
	ListDeliveriesForMessage(ctx context.Context, roomID, messageID string) ([]Delivery, error)    // by agent handle; empty, not nil
	ListDeliveriesInRange(ctx context.Context, roomID, firstID, lastID string) ([]Delivery, error) // by message id, then handle; empty, not nil
	HeadWaiting(ctx context.Context, roomID, agentID string) (Delivery, bool, error)               // the pair's releasable head; false when none
	ReleasableDeliveries(ctx context.Context, agentID string) ([]Delivery, error)                  // each pair's head, by room id; empty, not nil
	SetDeliveryState(ctx context.Context, roomID, agentID, messageID, state string) error          // ErrNotFound
	CancelDeliveries(ctx context.Context, roomID, agentID string) (int64, error)                   // waiting → cancelled; rows touched
	ListAgentRoomIDs(ctx context.Context, agentID string) ([]string, error)                        // ordered by id; empty, not nil

	PutActivity(ctx context.Context, a Activity) error                                        // ErrConflict on duplicate id
	GetActivity(ctx context.Context, id string) (Activity, error)                             // ErrNotFound
	SetActivityResult(ctx context.Context, id string, durationMs int64, succeeded bool) error // ErrNotFound, also for a remark
	ListActivities(ctx context.Context, roomID, messageID string) ([]Activity, error)         // oldest first; empty, not nil

	PutPrompt(ctx context.Context, p Prompt) error                                            // ErrConflict on a second prompt for the message; only the first five fields are written: a new prompt is open
	GetPrompt(ctx context.Context, messageID string) (Prompt, error)                          // ErrNotFound
	ListPromptsInRange(ctx context.Context, roomID, firstID, lastID string) ([]Prompt, error) // by message id; empty, not nil
	ListOpenPrompts(ctx context.Context, roomID string) ([]Prompt, error)                     // roomID "" means every room; by message id; empty, not nil
	CountOpenPrompts(ctx context.Context, roomID string) (int64, error)
	ClosePrompt(ctx context.Context, messageID, state, answer string, answeredBy *string, closedAtMs int64) error // ErrNotFound when there is no open prompt for the message

	GetNoteText(ctx context.Context, humanID string) (string, error)                  // "" when the human has written none
	PutNoteText(ctx context.Context, humanID, text string, updatedAtMs int64) error   // replaces
	PutNoteEntry(ctx context.Context, e NoteEntry) error                              // ErrConflict on duplicate id
	GetNoteEntry(ctx context.Context, id string) (NoteEntry, error)                   // ErrNotFound
	ListNoteEntries(ctx context.Context, humanID string) ([]NoteEntry, error)         // oldest first, pending and approved; empty, not nil
	SetNoteEntryText(ctx context.Context, id, text string) error                      // ErrNotFound
	SetNoteEntryApprovedAt(ctx context.Context, id string, approvedAtMs *int64) error // ErrNotFound; nil returns it to quarantine
	DeleteNoteEntry(ctx context.Context, id string) error                             // ErrNotFound
}

type repo struct {
	db     *DB
	reads  *sqlcgen.Queries
	writes *sqlcgen.Queries
	inTx   bool
}

// NewRepo returns a Repository over db.
func NewRepo(db *DB) *repo {
	return &repo{db: db, reads: sqlcgen.New(db.read), writes: sqlcgen.New(db.write)}
}

func (r *repo) Ping(ctx context.Context) error {
	if r.inTx {
		// The write connection is held by the transaction; pinging the pools would block.
		return ctx.Err()
	}
	if err := r.db.read.PingContext(ctx); err != nil {
		return fmt.Errorf("ping read pool: %w", err)
	}
	if err := r.db.write.PingContext(ctx); err != nil {
		return fmt.Errorf("ping write pool: %w", err)
	}
	return nil
}

func (r *repo) InTx(ctx context.Context, fn func(Repository) error) error {
	if r.inTx {
		return ErrNestedTx
	}
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Runs on error and on panic; releases the only write connection.
			_ = tx.Rollback()
		}
	}()
	q := r.writes.WithTx(tx)
	if err := fn(&repo{db: r.db, reads: q, writes: q, inTx: true}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

// mapErr translates driver errors into the package's sentinel errors.
func mapErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqliteConstraintUnique, sqliteConstraintPrimaryKey:
			return fmt.Errorf("%w: %w", ErrConflict, err)
		}
	}
	return err
}

// requireRow maps an UPDATE or DELETE that touched no rows to ErrNotFound.
func requireRow(n int64, err error) error {
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
