// Package livenotify tells connected clients the moment a notification is
// created for their user. Postgres NOTIFY carries the event from whichever
// transaction created it; the Hub fans it out to that user's open sockets.
package livenotify

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"aagasa/internal/store"
)

// Event is what a subscriber receives. NotificationID is 0 for a "resync"
// event, sent after the listener reconnects and may have missed something.
type Event struct {
	NotificationID int64
}

// Hub is safe for concurrent use. Its zero value is not usable; call New.
type Hub struct {
	mu        sync.Mutex
	subs      map[uuid.UUID]map[chan Event]struct{}
	done      chan struct{}
	stopOnce  sync.Once
	ready     chan struct{}
	readyOnce sync.Once
}

func New() *Hub {
	return &Hub{
		subs:  make(map[uuid.UUID]map[chan Event]struct{}),
		done:  make(chan struct{}),
		ready: make(chan struct{}),
	}
}

// Done is closed when the hub stops (the server is shutting down), so open
// connections can close cleanly; http.Server.Shutdown does not track them.
func (h *Hub) Done() <-chan struct{} { return h.done }

// Ready is closed once the first LISTEN succeeded.
func (h *Hub) Ready() <-chan struct{} { return h.ready }

func (h *Hub) stop() { h.stopOnce.Do(func() { close(h.done) }) }

// Subscribe registers a listener for one user. Call the returned function to
// unsubscribe. The channel is buffered; a slow consumer drops events rather
// than blocking everyone, which is fine because clients refetch on any event.
func (h *Hub) Subscribe(userID uuid.UUID) (<-chan Event, func()) {
	ch := make(chan Event, 8)
	h.mu.Lock()
	if h.subs[userID] == nil {
		h.subs[userID] = make(map[chan Event]struct{})
	}
	h.subs[userID][ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if set, ok := h.subs[userID]; ok {
			delete(set, ch)
			if len(set) == 0 {
				delete(h.subs, userID)
			}
		}
	}
}

// Publish delivers an event to every open connection of one user.
func (h *Hub) Publish(userID uuid.UUID, event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[userID] {
		select {
		case ch <- event:
		default:
		}
	}
}

func (h *Hub) broadcast(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, set := range h.subs {
		for ch := range set {
			select {
			case ch <- event:
			default:
			}
		}
	}
}

// Subscribers is the number of open connections, for tests and logs.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, set := range h.subs {
		n += len(set)
	}
	return n
}

// Listen holds a dedicated Postgres connection on LISTEN until ctx ends,
// reconnecting with backoff. After every reconnect it broadcasts a resync so
// clients refetch anything sent while it was away.
func (h *Hub) Listen(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	defer h.stop()
	backoff := time.Second
	first := true
	for ctx.Err() == nil {
		err := h.listenOnce(ctx, pool, logger, func() {
			backoff = time.Second
			h.readyOnce.Do(func() { close(h.ready) })
			if !first {
				h.broadcast(Event{})
			}
			first = false
		})
		if ctx.Err() != nil {
			return
		}
		logger.Warn("notification listener lost its connection; retrying",
			slog.String("error", errString(err)), slog.Duration("retry_in", backoff))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (h *Hub) listenOnce(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger,
	connected func()) error {
	pooled, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// LISTEN state belongs to the connection, so take it out of the pool for
	// good instead of handing it back.
	conn := pooled.Hijack()
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN "+store.NotificationChannel); err != nil {
		return err
	}
	logger.Info("notification listener ready")
	connected()

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		userPart, idPart, ok := strings.Cut(n.Payload, ":")
		if !ok {
			continue
		}
		userID, err := uuid.Parse(userPart)
		if err != nil {
			continue
		}
		id, _ := strconv.ParseInt(idPart, 10, 64)
		h.Publish(userID, Event{NotificationID: id})
	}
}

func errString(err error) string {
	if err == nil {
		return "connection closed"
	}
	return err.Error()
}
