package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"aagasa/internal/auth"
	"aagasa/internal/domain"
)

// Close codes in the 4000 range are application defined.
const (
	closeUnauthenticated   websocket.StatusCode = 4401
	closePasswordChange    websocket.StatusCode = 4403
	streamAuthTimeout                           = 10 * time.Second
	streamKeepAlive                             = 30 * time.Second
	streamWriteTimeout                          = 10 * time.Second
	streamMaxClientMessage                      = 4096
)

type streamMessage struct {
	Type  string `json:"type"`
	Token string `json:"token,omitempty"`
	ID    int64  `json:"id,omitempty"`
}

// OriginPatterns turns the allowed browser origins (full URLs) into the host
// patterns the WebSocket handshake checks. Same-origin requests and clients
// that send no Origin (the Android app) are always accepted.
func OriginPatterns(options CORSOptions) []string {
	var patterns []string
	for _, origin := range options.AllowedOrigins {
		if parsed, err := url.Parse(origin); err == nil && parsed.Host != "" {
			patterns = append(patterns, parsed.Host)
		}
	}
	if options.AllowLocalhost {
		patterns = append(patterns, "localhost", "localhost:*", "127.0.0.1", "127.0.0.1:*", "[::1]:*")
	}
	return patterns
}

// GET /api/notifications/stream
//
// A WebSocket that says "something new" the moment a notification is
// created for the user; the client then fetches /api/notifications. The
// client authenticates with its first message, {"type":"auth","token":...},
// because browsers cannot set headers on a WebSocket and a token in the URL
// would end up in logs. The server then sends {"type":"ready"}, and later
// {"type":"notification","id":N} or {"type":"resync"}.
func (s *Server) handleNotificationStream(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeError(s.logger, w, http.StatusServiceUnavailable, "unavailable",
			"live notifications are not enabled")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: s.wsOrigins,
	})
	if err != nil {
		return // Accept already wrote the response.
	}
	defer conn.CloseNow()
	conn.SetReadLimit(streamMaxClientMessage)

	ctx := r.Context()
	token, user, ok := s.authenticateStream(ctx, conn)
	if !ok {
		return
	}

	events, unsubscribe := s.live.Subscribe(user.ID)
	defer unsubscribe()

	// Reading is required for pings and close frames to be processed; the
	// client has nothing more to say, so anything it sends is ignored.
	ctx = conn.CloseRead(ctx)

	if err := writeStream(ctx, conn, streamMessage{Type: "ready"}); err != nil {
		return
	}

	keepAlive := time.NewTicker(streamKeepAlive)
	defer keepAlive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.live.Done():
			conn.Close(websocket.StatusGoingAway, "server shutting down")
			return
		case event := <-events:
			message := streamMessage{Type: "notification", ID: event.NotificationID}
			if event.NotificationID == 0 {
				message = streamMessage{Type: "resync"}
			}
			if err := writeStream(ctx, conn, message); err != nil {
				return
			}
		case <-keepAlive.C:
			// A signed-out or expired session ends the stream.
			if _, err := s.auth.Resolve(ctx, token); err != nil {
				if errors.Is(err, auth.ErrUnauthenticated) {
					conn.Close(closeUnauthenticated, "session ended")
				}
				return
			}
			pingCtx, cancel := context.WithTimeout(ctx, streamWriteTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) authenticateStream(ctx context.Context, conn *websocket.Conn) (string, domain.User, bool) {
	authCtx, cancel := context.WithTimeout(ctx, streamAuthTimeout)
	defer cancel()

	kind, data, err := conn.Read(authCtx)
	if err != nil {
		conn.Close(closeUnauthenticated, "authentication required")
		return "", domain.User{}, false
	}
	var hello streamMessage
	if kind != websocket.MessageText || json.Unmarshal(data, &hello) != nil ||
		hello.Type != "auth" || hello.Token == "" {
		conn.Close(closeUnauthenticated, "authentication required")
		return "", domain.User{}, false
	}

	user, err := s.auth.Resolve(ctx, hello.Token)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthenticated) {
			s.logger.Error("resolve stream session", slog.String("error", err.Error()))
			conn.Close(websocket.StatusInternalError, "try again")
			return "", domain.User{}, false
		}
		conn.Close(closeUnauthenticated, "authentication required")
		return "", domain.User{}, false
	}
	if user.MustChangePassword {
		conn.Close(closePasswordChange, "change your password first")
		return "", domain.User{}, false
	}
	return hello.Token, user, true
}

func writeStream(ctx context.Context, conn *websocket.Conn, message streamMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, streamWriteTimeout)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}
