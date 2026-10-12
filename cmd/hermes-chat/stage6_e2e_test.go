// Package main: end-to-end coverage of stage 6 with a real server and the fake agent.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/sse"
)

// data decodes one frame's JSON data.
func data(t *testing.T, f sse.Frame) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(f.Data), &m), f.Data)
	return m
}

func TestStage6E2E_AnApprovalWaitsForTheHuman(t *testing.T) {
	w := newAgentWorld(t)
	room := openDM(t, w, w.scoutID)
	scout, stream := w.agent(), openAgent(t, w)
	bg := context.Background()
	m1 := post(t, w, room, "clean the build")
	frameOf(t, stream, "delivery")
	require.NoError(t, scout.TurnWorking(bg, m1))

	ask, err := scout.Prompt(bg, room, "approval", "run `rm -rf build`?", []string{"once", "deny"})
	require.NoError(t, err)
	_, got := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room, "")
	assert.EqualValues(t, 1, got["open_prompts"], "the badge")

	code, answered := apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/messages/"+ask+"/answer", `{"answer":"once"}`)
	require.Equal(t, 200, code)
	assert.Equal(t, "answered", answered["prompt"].(map[string]any)["state"])
	assert.JSONEq(t, fmt.Sprintf(`{"room_id":%q,"message_id":%q,"answer":"once"}`, room, ask), frameOf(t, stream, "prompt.answered").Data)
	_, got = apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room, "")
	assert.EqualValues(t, 0, got["open_prompts"])

	// a second question, and the human hits pause instead of answering
	second, err := scout.Prompt(bg, room, "clarify", "delete the cache too?", []string{"yes", "no"})
	require.NoError(t, err)
	code, _ = apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/pause", "")
	require.Equal(t, 200, code)
	frameOf(t, stream, "pause")
	code, body := apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/messages/"+second+"/answer", `{"answer":"yes"}`)
	assert.Equal(t, 409, code)
	assert.Equal(t, "prompt_closed", body["error"].(map[string]any)["code"])
	_, err = scout.Prompt(bg, room, "clarify", "hello?", nil)
	assert.ErrorContains(t, err, "409 paused")
	assert.NoError(t, scout.ExpirePrompt(bg, second), "the plugin may still tidy up")
}

func TestStage6E2E_ASuggestionReachesAgentsOnlyOnceApproved(t *testing.T) {
	w := newAgentWorld(t)
	room := openDM(t, w, w.scoutID)
	scout, stream := w.agent(), openAgent(t, w)
	bg := context.Background()
	turn := func(text string) (id string, note any) {
		id = post(t, w, room, text)
		note = data(t, frameOf(t, stream, "delivery"))["note"]
		return
	}

	m1, note := turn("hello")
	assert.Nil(t, note)
	entry, err := scout.SuggestNote(bg, m1, "prefers trains")
	require.NoError(t, err)
	again, err := scout.SuggestNote(bg, m1, "prefers trains")
	require.NoError(t, err)
	assert.Equal(t, entry, again)
	require.NoError(t, scout.TurnDone(bg, m1))

	code, _ := apiCall(t, w.human, "PUT", w.api, "/api/note", `{"text":"I live in Lisbon."}`)
	require.Equal(t, 200, code)
	m2, note := turn("again")
	assert.Equal(t, "I live in Lisbon.", note, "the suggestion is still in quarantine")
	require.NoError(t, scout.TurnDone(bg, m2))

	code, n := apiCall(t, w.human, "PATCH", w.api, "/api/note/entries/"+entry, `{"approved":true}`)
	require.Equal(t, 200, code)
	assert.Len(t, n["entries"], 1)
	_, note = turn("and again")
	assert.Equal(t, "I live in Lisbon.\n\n- prefers trains", note)
}

func TestStage6E2E_AFileGoesUpAndOnlyMembersGetIt(t *testing.T) {
	w := newAgentWorld(t)
	runCmdIn(t, pw+"\n", "user", "add", "ada") // the CLI may run beside the server
	room := openDM(t, w, w.scoutID)
	scout := w.agent()
	bg := context.Background()
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)

	pic, err := scout.Upload(bg, room, "chart.png", strings.NewReader(png))
	require.NoError(t, err)
	page, err := scout.Upload(bg, room, "report.html", strings.NewReader("<html><script>alert(1)</script></html>"))
	require.NoError(t, err)
	msg, err := scout.PostMessageWith(bg, room, "the report", []string{pic, page})
	require.NoError(t, err)
	_, got := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room+"/messages/"+msg, "")
	assert.Len(t, got["attachments"], 2)

	fetch := func(client *http.Client, id string) *http.Response {
		resp, err := client.Get(w.api + "/api/rooms/" + room + "/attachments/" + id)
		require.NoError(t, err)
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	resp := fetch(w.human, pic)
	require.Equal(t, 200, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, png, string(body))
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	assert.Equal(t, "inline; filename=chart.png", resp.Header.Get("Content-Disposition"))
	resp = fetch(w.human, page)
	assert.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "attachment; filename=report.html", resp.Header.Get("Content-Disposition"))

	jar, _ := cookiejar.New(nil)
	ada := &http.Client{Jar: jar}
	require.Equal(t, 200, login(t, ada, w.api, "ada", pw).StatusCode)
	assert.Equal(t, 404, fetch(ada, pic).StatusCode)
	assert.Equal(t, 401, fetch(http.DefaultClient, pic).StatusCode)

	// the files sit beside the database, named by id
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(os.Getenv("HERMES_CHAT_DB_PATH")), "attachments"))
	require.NoError(t, err)
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{pic, page}, names)
}