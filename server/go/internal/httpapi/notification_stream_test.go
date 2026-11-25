package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type streamClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func (e *testEnv) openStream(t *testing.T) *streamClient {
	t.Helper()
	select {
	case <-e.live.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("notification listener never became ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(e.server.URL, "http") + "/api/notifications/stream"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial stream: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return &streamClient{t: t, conn: conn}
}

func (c *streamClient) send(message map[string]any) {
	c.t.Helper()
	data, _ := json.Marshal(message)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

// next returns the next message, or the close status if the server closed.
func (c *streamClient) next() (map[string]any, websocket.StatusCode) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		if status := websocket.CloseStatus(err); status != -1 {
			return nil, status
		}
		c.t.Fatalf("read: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(data, &message); err != nil {
		c.t.Fatalf("decode %q: %v", data, err)
	}
	return message, -1
}

func TestNotificationStreamPushesTheMomentAPassIsRequested(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "operator", "user")
	_, adminToken := env.createUser(t, rootToken, "approver", "admin")

	admin := env.openStream(t)
	admin.send(map[string]any{"type": "auth", "token": adminToken})
	if message, status := admin.next(); message["type"] != "ready" {
		t.Fatalf("first message = %v (close %d), want ready", message, status)
	}

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))
	if code, body := env.schedulePass(t, userToken, satelliteID, aos, nil); code != http.StatusCreated {
		t.Fatalf("request pass = %d %v", code, body)
	}

	message, status := admin.next()
	if message["type"] != "notification" {
		t.Fatalf("pushed = %v (close %d), want a notification", message, status)
	}
	items, _ := notificationsOf(t, env, adminToken, "")
	if len(items) != 1 || items[0]["id"] != message["id"] {
		t.Errorf("pushed id %v does not match the stored notification %v", message["id"], items)
	}
}

func TestNotificationStreamRejectsABadToken(t *testing.T) {
	env := newTestEnv(t)
	client := env.openStream(t)
	client.send(map[string]any{"type": "auth", "token": "not-a-session"})
	if _, status := client.next(); status != 4401 {
		t.Errorf("close status = %d, want 4401", status)
	}
}

func TestNotificationStreamRequiresAuthFirst(t *testing.T) {
	env := newTestEnv(t)
	client := env.openStream(t)
	client.send(map[string]any{"type": "hello"})
	if _, status := client.next(); status != 4401 {
		t.Errorf("close status = %d, want 4401", status)
	}
}
