package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"aagasa/internal/domain"
	"aagasa/internal/store"
)

var (
	// ErrInvalidCredentials is returned for any failed login. It deliberately
	// does not distinguish an unknown user from a wrong password.
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrPasswordChangeRequired means the caller must change their password
	// before doing anything else.
	ErrPasswordChangeRequired = errors.New("password change required")
	// ErrForbidden means the caller is authenticated but not permitted.
	ErrForbidden = errors.New("not permitted")
	// ErrUnauthenticated means no valid session was presented.
	ErrUnauthenticated = errors.New("authentication required")
)

// Service owns credentials and sessions.
type Service struct {
	repo       *store.Repository
	sessions   *store.SessionStore
	logger     *slog.Logger
	sessionTTL time.Duration
}

// NewService builds the authentication service.
func NewService(repo *store.Repository, sessions *store.SessionStore, logger *slog.Logger, sessionTTL time.Duration) *Service {
	return &Service{repo: repo, sessions: sessions, logger: logger, sessionTTL: sessionTTL}
}

// EnsureBootstrapRoot creates the initial Root account if no user exists.
//
// spec.md section 17.1 fixes the first-run credentials as root/toor with an
// immediate forced password change. Nothing else works until that happens.
func (s *Service) EnsureBootstrapRoot(ctx context.Context) error {
	count, err := s.repo.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return nil
	}

	hash, err := HashPassword(BootstrapPassword)
	if err != nil {
		return err
	}
	root, err := s.repo.CreateUser(ctx, domain.User{
		Username:           BootstrapUsername,
		PasswordHash:       hash,
		Role:               domain.RoleRoot,
		MustChangePassword: true,
	})
	if err != nil {
		return fmt.Errorf("create root: %w", err)
	}

	// Never log the credential itself, only that bootstrap happened.
	s.logger.Warn("bootstrap root account created; password change is required before use")
	_, err = s.repo.AppendAudit(ctx, domain.AuditRecord{
		Action: "user.bootstrap_created", EntityType: "user", EntityID: root.ID.String(),
		NewState: map[string]any{"username": root.Username, "role": string(root.Role)},
	})
	return err
}

// LoginResult carries the outcome of a successful authentication.
type LoginResult struct {
	Token              string
	User               domain.User
	MustChangePassword bool
	ExpiresAt          time.Time
}

// Login verifies credentials and issues a session.
//
// A user with must_change_password still receives a session: they need one to
// call ChangePassword. Every other operation is refused until they do.
func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	user, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Spend comparable time on an unknown user so the response time
			// does not reveal whether the account exists.
			_ = VerifyPassword(password, decoyHash)
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, err
	}

	if err := VerifyPassword(password, user.PasswordHash); err != nil {
		if errors.Is(err, ErrInvalidHash) {
			s.logger.Error("stored password hash is unusable", slog.String("user_id", user.ID.String()))
		}
		s.audit(ctx, nil, "auth.login_failed", "user", user.ID.String(), nil)
		return LoginResult{}, ErrInvalidCredentials
	}

	token, err := store.NewToken()
	if err != nil {
		return LoginResult{}, err
	}
	now := time.Now().UTC()
	session := store.Session{UserID: user.ID, Role: string(user.Role), CreatedAt: now}
	if err := s.sessions.Create(ctx, token, session, s.sessionTTL); err != nil {
		return LoginResult{}, err
	}

	s.audit(ctx, &user.ID, "auth.login", "user", user.ID.String(), nil)

	return LoginResult{
		Token:              token,
		User:               user,
		MustChangePassword: user.MustChangePassword,
		ExpiresAt:          now.Add(s.sessionTTL),
	}, nil
}

// decoyHash makes an unknown-user login cost roughly the same as a real one.
// It is a valid Argon2id hash of a value nobody can supply.
var decoyHash = mustHash()

func mustHash() string {
	hash, err := HashPassword("unused-decoy-password-value")
	if err != nil {
		panic("auth: cannot build decoy hash: " + err.Error())
	}
	return hash
}

// Logout revokes a session token.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.sessions.Delete(ctx, token)
}

// Resolve turns a session token into its user.
func (s *Service) Resolve(ctx context.Context, token string) (domain.User, error) {
	session, err := s.sessions.Get(ctx, token)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return domain.User{}, ErrUnauthenticated
		}
		return domain.User{}, err
	}

	user, err := s.repo.GetUserByID(ctx, session.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// The account was removed while the session was still alive.
			_ = s.sessions.Delete(ctx, token)
			return domain.User{}, ErrUnauthenticated
		}
		return domain.User{}, err
	}
	return user, nil
}

// ChangePassword replaces a user's password after checking the current one.
//
// The forced-change flag is cleared, which is what retires the bootstrap
// credential (spec.md section 17.1).
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error {
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := VerifyPassword(currentPassword, user.PasswordHash); err != nil {
		s.audit(ctx, &userID, "auth.password_change_failed", "user", userID.String(), nil)
		return ErrInvalidCredentials
	}
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}

	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.repo.SetPassword(ctx, userID, hash); err != nil {
		return err
	}

	s.audit(ctx, &userID, "auth.password_changed", "user", userID.String(), nil)
	return nil
}

// audit records a security event, never the credential involved.
func (s *Service) audit(ctx context.Context, actor *uuid.UUID, action, entityType, entityID string, details map[string]any) {
	if _, err := s.repo.AppendAudit(ctx, domain.AuditRecord{
		ActorUserID: actor, Action: action, EntityType: entityType,
		EntityID: entityID, NewState: details,
	}); err != nil {
		s.logger.Error("audit write failed", slog.String("action", action), slog.String("error", err.Error()))
	}
}
