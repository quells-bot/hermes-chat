package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

type PromptKind string

const (
	PromptApproval PromptKind = "approval"
	PromptClarify  PromptKind = "clarify"
)

type PromptState string

const (
	PromptOpen     PromptState = "open"
	PromptAnswered PromptState = "answered"
	PromptExpired  PromptState = "expired"
)

// Prompt is what the plugin is waiting on: an approval, or a clarifying
// question with choices. It rides the asking message.
type Prompt struct {
	Kind       PromptKind
	Choices    []string // never nil
	State      PromptState
	Answer     string    // "" unless answered
	AnsweredBy *Human    // nil unless answered, and once that human is removed
	ClosedAt   time.Time // zero while open
}

const (
	MaxPromptChoices     = 6
	MaxPromptChoiceChars = 200
	MaxPromptAnswerChars = 2000
)

var (
	errPromptKind     = ValidationError{Message: "kind must be approval or clarify"}
	errPromptApproval = ValidationError{Message: "an approval's choices must be one or more of once, session, always, deny, each at most once"}
	errPromptClarify  = ValidationError{Message: "a clarifying question takes at most 6 choices, each 1-200 characters with no control characters, none repeated"}
	errPromptAnswer   = ValidationError{Message: "answer must be one of the prompt's choices"}
	errClarifyAnswer  = ValidationError{Message: "answer must be 1-2000 characters with no NUL character"}
)

// validatePromptChoices checks and trims the choices for a prompt of kind.
// An approval's choices must be a non-empty subset of the four; a clarifying
// question's are 0 to 6 free strings.
func validatePromptChoices(kind PromptKind, choices []string) ([]string, error) {
	if kind == PromptApproval {
		if len(choices) < 1 {
			return nil, errPromptApproval
		}
		seen := map[string]bool{}
		out := make([]string, len(choices))
		for i, c := range choices {
			switch c {
			case "once", "session", "always", "deny":
			default:
				return nil, errPromptApproval
			}
			if seen[c] {
				return nil, errPromptApproval
			}
			seen[c] = true
			out[i] = c
		}
		return out, nil
	}
	if len(choices) > MaxPromptChoices {
		return nil, errPromptClarify
	}
	seen := map[string]bool{}
	out := []string{}
	for _, c := range choices {
		t := strings.TrimSpace(c)
		n := utf8.RuneCountInString(t)
		if n < 1 || n > MaxPromptChoiceChars || strings.IndexFunc(t, unicode.IsControl) >= 0 || seen[t] {
			return nil, errPromptClarify
		}
		seen[t] = true
		out = append(out, t)
	}
	return out, nil
}

// validatePromptAnswer checks the trimmed answer against the prompt's kind.
func validatePromptAnswer(p Prompt, trimmed string) error {
	if p.Kind == PromptApproval {
		for _, c := range p.Choices {
			if trimmed == c {
				return nil
			}
		}
		return errPromptAnswer
	}
	n := utf8.RuneCountInString(trimmed)
	if n < 1 || n > MaxPromptAnswerChars || strings.ContainsRune(trimmed, 0) {
		return errClarifyAnswer
	}
	return nil
}

// promptFromStorage maps a stored prompt to the bus's, decoding its choices
// and resolving the human who answered it; a human since removed leaves
// AnsweredBy nil.
func promptFromStorage(ctx context.Context, r storage.Repository, p storage.Prompt) (Prompt, error) {
	var choices []string
	if err := json.Unmarshal([]byte(p.Choices), &choices); err != nil {
		return Prompt{}, fmt.Errorf("stored prompt %s has invalid choices: %w", p.MessageID, err)
	}
	if choices == nil {
		choices = []string{}
	}
	prompt := Prompt{Kind: PromptKind(p.Kind), Choices: choices, State: PromptState(p.State), Answer: p.Answer}
	if p.ClosedAtMs != nil {
		prompt.ClosedAt = time.UnixMilli(*p.ClosedAtMs).UTC()
	}
	if p.AnsweredBy != nil {
		row, err := r.GetHumanByID(ctx, *p.AnsweredBy)
		if err == nil {
			if h, err := humanFromRow(row); err == nil {
				cp := h
				prompt.AnsweredBy = &cp
			}
		}
	}
	return prompt, nil
}

// loadPrompts maps the rows' message ids to their prompts, one range query
// over the rows, as loadDeliveries does for deliveries.
func loadPrompts(ctx context.Context, r storage.Repository, rows []storage.Message) (map[string]Prompt, error) {
	if len(rows) == 0 {
		return map[string]Prompt{}, nil
	}
	first, last := rows[0].ID, rows[0].ID
	for _, row := range rows {
		if row.ID < first {
			first = row.ID
		}
		if row.ID > last {
			last = row.ID
		}
	}
	ps, err := r.ListPromptsInRange(ctx, rows[0].RoomID, first, last)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	out := map[string]Prompt{}
	for _, p := range ps {
		prompt, err := promptFromStorage(ctx, r, p)
		if err != nil {
			return nil, err
		}
		out[p.MessageID] = prompt
	}
	return out, nil
}

// PostPrompt posts an agent's question as a message with a prompt beside it.
// The message wakes nobody: the human sees it on their stream, and the badge.
func (s *service) PostPrompt(ctx context.Context, actor Actor, roomID ulid.ULID, kind PromptKind, text string, choices []string) (Message, error) {
	if actor.Kind != ActorAgent {
		return Message{}, ErrForbidden
	}
	var kindOK bool
	switch kind {
	case PromptApproval, PromptClarify:
		kindOK = true
	}
	if !kindOK {
		return Message{}, errPromptKind
	}
	if !validMessageText(text) {
		return Message{}, errMessageText
	}
	trimmed, err := validatePromptChoices(kind, choices)
	if err != nil {
		return Message{}, err
	}
	choicesJSON, err := json.Marshal(trimmed)
	if err != nil {
		return Message{}, fmt.Errorf("encode choices: %w", err)
	}
	var posted Message
	err = s.record(ctx, func(r storage.Repository) error {
		row, _, err := roomForPoster(ctx, r, actor, roomID)
		if err != nil {
			return err
		}
		if row.ArchivedAtMs != nil {
			return ErrRoomArchived
		}
		if closed, err := paused(ctx, r, row); err != nil {
			return err
		} else if closed {
			return ErrPaused
		}
		at := s.now()
		id, err := newID(at)
		if err != nil {
			return fmt.Errorf("generate id: %w", err)
		}
		agent := actor.ID.String()
		msg := storage.Message{ID: id.String(), RoomID: row.ID, AuthorAgentID: &agent, Body: text, CreatedAtMs: at.UnixMilli()}
		if err := r.PutMessage(ctx, msg); err != nil {
			return fmt.Errorf("put message: %w", err)
		}
		if err := r.PutPrompt(ctx, storage.Prompt{
			MessageID: msg.ID, RoomID: row.ID, AgentID: actor.ID.String(), Kind: string(kind), Choices: string(choicesJSON),
		}); err != nil {
			return fmt.Errorf("put prompt: %w", err)
		}
		if err := putEvent(ctx, r, EventMessagePosted, row.ID, msg.ID, at); err != nil {
			return err
		}
		if err := putEvent(ctx, r, EventRoomChanged, row.ID, row.ID, at); err != nil {
			return err
		}
		built, err := messagesFromRows(ctx, r, []storage.Message{msg})
		if err != nil {
			return err
		}
		posted = built[0]
		return nil
	})
	if err != nil {
		return Message{}, wrapOp("post prompt", err)
	}
	return posted, nil
}

// AnswerPrompt answers an open prompt in a room the human belongs to, and
// hands the answer to the asking agent. Answering an already answered prompt
// with the same answer changes nothing; any other answer, or any answer to an
// expired prompt, is ErrPromptClosed.
func (s *service) AnswerPrompt(ctx context.Context, actor Actor, roomID, messageID ulid.ULID, answer string) (Message, error) {
	var got Message
	err := s.record(ctx, func(r storage.Repository) error {
		row, err := roomForMember(ctx, r, actor, roomID)
		if err != nil {
			return err
		}
		mrow, found, err := messageInRoom(ctx, r, roomID, messageID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("message %s: %w", messageID, ErrNotFound)
		}
		p, err := r.GetPrompt(ctx, mrow.ID)
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("message %s: %w", messageID, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("get prompt: %w", err)
		}
		trimmed := strings.TrimSpace(answer)
		if p.State == string(PromptAnswered) && p.Answer == trimmed {
			// a double tap with the same answer is quiet: nothing is written
			built, err := messagesFromRows(ctx, r, []storage.Message{mrow})
			if err != nil {
				return err
			}
			got = built[0]
			return nil
		}
		if p.State != string(PromptOpen) {
			return ErrPromptClosed
		}
		if row.ArchivedAtMs != nil {
			return ErrRoomArchived
		}
		if closed, err := paused(ctx, r, row); err != nil {
			return err
		} else if closed {
			return ErrPaused
		}
		prompt, err := promptFromStorage(ctx, r, p)
		if err != nil {
			return err
		}
		if err := validatePromptAnswer(prompt, trimmed); err != nil {
			return err
		}
		at := s.now()
		answeredBy := actor.ID.String()
		if err := r.ClosePrompt(ctx, p.MessageID, string(PromptAnswered), trimmed, &answeredBy, at.UnixMilli()); err != nil {
			return fmt.Errorf("close prompt: %w", err)
		}
		if err := putEvent(ctx, r, EventPromptChanged, row.ID, mrow.ID, at); err != nil {
			return err
		}
		if err := putEvent(ctx, r, EventRoomChanged, row.ID, row.ID, at); err != nil {
			return err
		}
		agentID, err := ulid.ParseStrict(p.AgentID)
		if err != nil {
			return fmt.Errorf("stored agent has an invalid id %q: %w", p.AgentID, err)
		}
		if err := putAgentEvent(ctx, r, EventPromptAnswered, row.ID, mrow.ID, agentID, at); err != nil {
			return err
		}
		built, err := messagesFromRows(ctx, r, []storage.Message{mrow})
		if err != nil {
			return err
		}
		got = built[0]
		return nil
	})
	if err != nil {
		return Message{}, wrapOp("answer prompt", err)
	}
	return got, nil
}

// ExpirePrompt closes a prompt the asking agent gave up on. It is bookkeeping,
// like a turn report: it checks neither membership nor the off switch.
func (s *service) ExpirePrompt(ctx context.Context, actor Actor, messageID ulid.ULID) error {
	at := s.now()
	err := s.record(ctx, func(r storage.Repository) error {
		p, err := r.GetPrompt(ctx, messageID.String())
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("prompt %s: %w", messageID, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("get prompt: %w", err)
		}
		if p.AgentID != actor.ID.String() {
			return fmt.Errorf("prompt %s: %w", messageID, ErrNotFound)
		}
		if p.State != string(PromptOpen) {
			return nil // already answered or expired: the first outcome stands
		}
		if err := r.ClosePrompt(ctx, p.MessageID, string(PromptExpired), "", nil, at.UnixMilli()); err != nil {
			return fmt.Errorf("close prompt: %w", err)
		}
		if err := putEvent(ctx, r, EventPromptChanged, p.RoomID, p.MessageID, at); err != nil {
			return err
		}
		if err := putEvent(ctx, r, EventRoomChanged, p.RoomID, p.RoomID, at); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return wrapOp("expire prompt", err)
	}
	return nil
}

// expireOpenPrompts expires the open prompts of a room ("" means every room)
// asked by an agent ("" means any), writing one prompt.changed per prompt. It
// reports how many it expired; the caller writes room.changed.
func (s *service) expireOpenPrompts(ctx context.Context, r storage.Repository, roomID, agentID string, at time.Time) (int, error) {
	prompts, err := r.ListOpenPrompts(ctx, roomID)
	if err != nil {
		return 0, fmt.Errorf("list open prompts: %w", err)
	}
	n := 0
	for _, p := range prompts {
		if agentID != "" && p.AgentID != agentID {
			continue
		}
		if err := r.ClosePrompt(ctx, p.MessageID, string(PromptExpired), "", nil, at.UnixMilli()); err != nil {
			return 0, fmt.Errorf("close prompt: %w", err)
		}
		if err := putEvent(ctx, r, EventPromptChanged, p.RoomID, p.MessageID, at); err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}