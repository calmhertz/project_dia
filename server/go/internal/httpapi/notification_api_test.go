package httpapi_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func notificationsOf(t *testing.T, env *testEnv, token, query string) ([]map[string]any, int) {
	t.Helper()
	code, body := env.do(t, http.MethodGet, "/api/notifications"+query, token, nil)
	if code != http.StatusOK {
		t.Fatalf("list notifications = %d %v", code, body)
	}
	raw := body["notifications"].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		items = append(items, item.(map[string]any))
	}
	return items, int(body["unread_count"].(float64))
}

func TestPassLifecycleNotifiesTheRightPeople(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "operator", "user")
	_, adminToken := env.createUser(t, rootToken, "approver", "admin")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))
	code, body := env.schedulePass(t, userToken, satelliteID, aos, nil)
	if code != http.StatusCreated {
		t.Fatalf("request pass = %d %v", code, body)
	}
	passID := body["pass"].(map[string]any)["id"].(string)

	// The admin hears about the request; the requester does not.
	adminItems, adminUnread := notificationsOf(t, env, adminToken, "")
	if len(adminItems) != 1 || adminItems[0]["kind"] != "pass.requested" || adminUnread != 1 {
		t.Fatalf("admin notifications = %v (unread %d)", adminItems, adminUnread)
	}
	if adminItems[0]["entity_id"] != passID {
		t.Errorf("entity_id = %v, want %s", adminItems[0]["entity_id"], passID)
	}
	if items, _ := notificationsOf(t, env, userToken, ""); len(items) != 0 {
		t.Errorf("requester notifications = %v, want none", items)
	}

	if code, body := env.do(t, http.MethodPost, "/api/passes/"+passID+"/approve", adminToken, nil); code != http.StatusOK {
		t.Fatalf("approve = %d %v", code, body)
	}

	userItems, userUnread := notificationsOf(t, env, userToken, "")
	if len(userItems) != 1 || userItems[0]["kind"] != "pass.approved" || userUnread != 1 {
		t.Fatalf("user notifications = %v (unread %d)", userItems, userUnread)
	}
	// The approver did not get a notification about their own decision.
	if items, _ := notificationsOf(t, env, adminToken, ""); len(items) != 1 {
		t.Errorf("admin notifications after approving = %v, want just the request", items)
	}

	// after_id returns only newer items.
	newest := int64(userItems[0]["id"].(float64))
	if items, _ := notificationsOf(t, env, userToken, "?after_id="+strconv.FormatInt(newest, 10)); len(items) != 0 {
		t.Errorf("after_id=%d returned %v", newest, items)
	}

	// Reading: someone else's notification is not found, own one is marked.
	path := "/api/notifications/" + strconv.FormatInt(newest, 10) + "/read"
	if code, _ := env.do(t, http.MethodPost, path, adminToken, nil); code != http.StatusNotFound {
		t.Errorf("admin reading user's notification = %d, want 404", code)
	}
	if code, _ := env.do(t, http.MethodPost, path, userToken, nil); code != http.StatusNoContent {
		t.Errorf("mark read = %d, want 204", code)
	}
	if _, unread := notificationsOf(t, env, userToken, ""); unread != 0 {
		t.Errorf("unread after read = %d, want 0", unread)
	}

	if code, _ := env.do(t, http.MethodPost, "/api/notifications/read-all", adminToken, nil); code != http.StatusNoContent {
		t.Errorf("read-all = %d, want 204", code)
	}
	if _, unread := notificationsOf(t, env, adminToken, ""); unread != 0 {
		t.Errorf("admin unread after read-all = %d, want 0", unread)
	}
}

func TestNotificationsRequireAuthentication(t *testing.T) {
	env := newTestEnv(t)
	if code, _ := env.do(t, http.MethodGet, "/api/notifications", "", nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous list = %d, want 401", code)
	}
}
