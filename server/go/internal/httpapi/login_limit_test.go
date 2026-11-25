package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"aagasa/internal/auth"
	"aagasa/internal/store"
)

func TestRepeatedFailedLoginsForOneUserAreThrottled(t *testing.T) {
	env := newTestEnv(t)
	env.rootReady(t)

	for attempt := 1; attempt <= store.MaxFailuresPerUser; attempt++ {
		if code, _, _ := env.login(t, auth.BootstrapUsername, "wrong-password"); code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d, want 401", attempt, code)
		}
	}

	code, body := env.do(t, http.MethodPost, "/api/auth/login", "", map[string]any{
		"username": auth.BootstrapUsername, "password": strongPassword,
	})
	if code != http.StatusTooManyRequests {
		t.Fatalf("login after %d failures = %d, want 429", store.MaxFailuresPerUser, code)
	}
	if body["error"] != "too_many_attempts" {
		t.Errorf("error = %v, want too_many_attempts", body["error"])
	}
	if message, _ := body["message"].(string); message == "" {
		t.Error("the 429 carries no message")
	}

	if code, _, _ := env.login(t, "someone-else", "wrong-password"); code != http.StatusUnauthorized {
		t.Errorf("another username from the same client = %d, want 401", code)
	}
}

func TestSuccessfulLoginClearsTheUserCounter(t *testing.T) {
	env := newTestEnv(t)
	env.rootReady(t)

	for round := 0; round < 3; round++ {
		for attempt := 0; attempt < store.MaxFailuresPerUser-1; attempt++ {
			if code, _, _ := env.login(t, auth.BootstrapUsername, "wrong-password"); code != http.StatusUnauthorized {
				t.Fatalf("round %d failure %d = %d, want 401", round, attempt, code)
			}
		}
		if code, _, _ := env.login(t, auth.BootstrapUsername, strongPassword); code != http.StatusOK {
			t.Fatalf("round %d: correct password = %d, want 200", round, code)
		}
	}
}

func TestOneClientGuessingManyUsernamesIsThrottled(t *testing.T) {
	env := newTestEnv(t)
	env.rootReady(t)

	for attempt := 0; attempt < store.MaxFailuresPerClient; attempt++ {
		username := "guess-" + string(rune('a'+attempt))
		if code, _, _ := env.login(t, username, "wrong-password"); code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d, want 401", attempt, code)
		}
	}

	code, body := env.do(t, http.MethodPost, "/api/auth/login", "", map[string]any{
		"username": auth.BootstrapUsername, "password": strongPassword,
	})
	if code != http.StatusTooManyRequests || body["error"] != "too_many_attempts" {
		t.Errorf("login after %d client failures = %d %v, want 429 too_many_attempts",
			store.MaxFailuresPerClient, code, body["error"])
	}
}

func TestForwardedForDoesNotEscapeTheClientLimit(t *testing.T) {
	env := newTestEnv(t)

	for attempt := 0; attempt <= store.MaxFailuresPerClient; attempt++ {
		request, err := http.NewRequest(http.MethodPost, env.server.URL+"/api/auth/login",
			strings.NewReader(`{"username":"guess-`+string(rune('a'+attempt))+`","password":"x"}`))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Forwarded-For", "203.0.113."+string(rune('0'+attempt%10)))
		response, err := env.server.Client().Do(request)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		_ = response.Body.Close()
		if attempt == store.MaxFailuresPerClient && response.StatusCode != http.StatusTooManyRequests {
			t.Errorf("a spoofed X-Forwarded-For escaped the limit: %d", response.StatusCode)
		}
	}
}
