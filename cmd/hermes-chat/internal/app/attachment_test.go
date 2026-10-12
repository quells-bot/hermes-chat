package app_test

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

var png = "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)

// uploadAs sends body as the agent's file; hdr is alternating header names and values.
func uploadAs(e *gin.Engine, token, room, name, body string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/agent/rooms/"+room+"/attachments?name="+url.QueryEscape(name), strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

func TestAttachments_UploadAndPost(t *testing.T) {
	r := newAgentRig(t)
	room := r.room.ID.String()
	w := uploadAs(r.e, r.scoutT, room, "cat.png", png, "Content-Type", "text/html")
	require.Equal(t, 201, w.Code, w.Body.String())
	a := obj(t, w)
	assert.Equal(t, "cat.png", a["name"])
	assert.Equal(t, "image/png", a["content_type"], "sniffed; the claimed type is ignored")
	assert.Equal(t, true, a["image"])
	assert.EqualValues(t, len(png), a["size"])

	w = agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+room+"/messages", fmt.Sprintf(`{"text":"","attachment_ids":[%q]}`, a["id"]))
	require.Equal(t, 201, w.Code, w.Body.String())
	assert.JSONEq(t, string(mustJSON(t, []any{a})), string(mustJSON(t, obj(t, w)["attachments"])))
	w = agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+room+"/messages", `{"text":"plain"}`)
	assert.Equal(t, []any{}, obj(t, w)["attachments"])

	assert.Equal(t, 400, uploadAs(r.e, r.scoutT, room, "", png).Code)
	assert.Equal(t, 400, uploadAs(r.e, r.scoutT, room, "empty.txt", "").Code)
	assert.Equal(t, 404, uploadAs(r.e, r.atlasT, room, "cat.png", png).Code)
	assert.Equal(t, 400, agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+room+"/messages", `{"text":"x","attachment_ids":["nope"]}`).Code)
	big := httptest.NewRequest("POST", "/agent/rooms/"+room+"/attachments?name=big.bin", strings.NewReader("x"))
	big.Header.Set("Authorization", "Bearer "+r.scoutT)
	big.ContentLength = bus.MaxAttachmentBytes + 1
	w = httptest.NewRecorder()
	r.e.ServeHTTP(w, big)
	assert.Equal(t, 413, w.Code, "refused on the declared length, before reading")
	assert.JSONEq(t, `{"error":{"code":"too_large","message":"the file is larger than 25 MiB"}}`, w.Body.String())
}

// posted has scout upload one file to its DM with kai and post it, and returns the download path.
func posted(t *testing.T, c chat, name, contentType, body string) string {
	t.Helper()
	kai, scout := bus.Actor{Kind: bus.ActorHuman, ID: c.kaiH.ID}, bus.Actor{Kind: bus.ActorAgent, ID: c.scout.ID}
	room, _, err := c.svc.OpenDM(ctx, kai, c.scout.ID)
	require.NoError(t, err)
	a, err := c.svc.UploadAttachment(ctx, scout, room.ID, name, contentType, strings.NewReader(body))
	require.NoError(t, err)
	_, err = c.svc.PostMessageWithAttachments(ctx, scout, room.ID, "", ulid.Zero, []ulid.ULID{a.ID})
	require.NoError(t, err)
	return "/api/rooms/" + room.ID.String() + "/attachments/" + a.ID.String()
}

func TestAttachments_ImagesAreServedInline(t *testing.T) {
	c := newChat(t)
	path := posted(t, c, "cat.png", "image/png", png)
	w := c.call(c.kai, "GET", path, "")
	require.Equal(t, 200, w.Code)
	assert.Equal(t, png, w.Body.String())
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "inline; filename=cat.png", w.Header().Get("Content-Disposition"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "sandbox", w.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "private, max-age=86400", w.Header().Get("Cache-Control"))

	assert.JSONEq(t, notFound, c.call(c.ada, "GET", path, "").Body.String(), "ada is not in the room")
	assert.Equal(t, 401, c.call("", "GET", path, "").Code)
	assert.JSONEq(t, notFound, c.call(c.kai, "GET", path[:len(path)-26]+"nope", "").Body.String())
}

// Review Focus 2.
func TestAttachments_UnsafeTypesAreDownloads(t *testing.T) {
	c := newChat(t)
	for _, f := range []struct{ name, stored, body string }{
		{"cat.png", "text/html; charset=utf-8", "<html><script>alert(1)</script></html>"},
		{"logo.svg", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`},
		{`we"ird; name.pdf`, "application/pdf", "%PDF-1.7"},
		{"résumé.pdf", "application/pdf", "%PDF-1.7"},
	} {
		w := c.call(c.kai, "GET", posted(t, c, f.name, f.stored, f.body), "")
		require.Equal(t, 200, w.Code, f.name)
		assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"), f.name)
		disposition, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
		require.NoError(t, err, f.name)
		assert.Equal(t, "attachment", disposition, f.name)
		assert.Equal(t, f.name, params["filename"], "the name survives the header intact")
		assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	}
	// what the upload route itself stores for a file that lies about being an image
	r := newAgentRig(t)
	w := uploadAs(r.e, r.scoutT, r.room.ID.String(), "cat.png", "<html><script>alert(1)</script></html>", "Content-Type", "image/png")
	require.Equal(t, 201, w.Code)
	assert.Equal(t, false, obj(t, w)["image"])
	assert.Equal(t, "text/html; charset=utf-8", obj(t, w)["content_type"])
	w = uploadAs(r.e, r.scoutT, r.room.ID.String(), "../../etc/passwd", "root:x:0:0", "Content-Type", "image/png")
	require.Equal(t, 201, w.Code)
	assert.Equal(t, "passwd", obj(t, w)["name"])
}

// Review Focus 3: the server's write timeout is counted from the request's headers, so a slow
// upload would otherwise get no response at all.
func TestAttachments_SlowUploadOutlivesTheWriteTimeout(t *testing.T) {
	r := newAgentRig(t)
	srv := httptest.NewUnstartedServer(r.e)
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(png[:8]))
		time.Sleep(400 * time.Millisecond) // well past the write timeout
		_, _ = pw.Write([]byte(png[8:]))
		pw.Close()
	}()
	req, _ := http.NewRequest("POST", srv.URL+"/agent/rooms/"+r.room.ID.String()+"/attachments?name=cat.png", pr)
	req.Header.Set("Authorization", "Bearer "+r.scoutT)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 201, resp.StatusCode)
}