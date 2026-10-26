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
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrPasswordChangeRequired = errors.New("password change required")
	ErrForbidden = errors.New("not permitted")
	ErrUnauthenticated = errors.New("authentication required")
)

type Service struct {
	repo       *store.Repository
	sessions   *store.SessionStore
	logger     *slog.Logger
	sessionTTL time.Duration
}

func NewService(repo *store.Repository, sessions *store.SessionStore, logger *slog.Logger, sessionTTL time.Duration) *Service {
	return &Service{repo: repo, sessions: sessions, logger: logger, sessionTTL: sessionTTL}
}

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

	s.logger.Warn("bootstrap root account created; password change is required before use")
	_, err = s.repo.AppendAudit(ctx, domain.AuditRecord{
		Action: "user.bootstrap_created", EntityType: "user", EntityID: root.ID.String(),
		NewState: map[string]any{"username": root.Username, "role": string(root.Role)},
	})
	return err
}

type LoginResult struct {
	Token              string
	User               domain.User
	MustChangePassword bool
	ExpiresAt          time.Time
}

func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	user, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
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

var decoyHash = mustHash()

func mustHash() string {
	hash, err := HashPassword("unused-decoy-password-value")
	if err != nil {
		panic("auth: cannot build decoy hash: " + err.Error())
	}
	return hash
}

func (s *Service) Logout(ctx context.Context, token string) error {
	return s.sessions.Delete(ctx, token)
}

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
			_ = s.sessions.Delete(ctx, token)
			return domain.User{}, ErrUnauthenticated
		}
		return domain.User{}, err
	}
	return user, nil
}

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

func (s *Service) audit(ctx context.Context, actor *uuid.UUID, action, entityType, entityID string, details map[string]any) {
	if _, err := s.repo.AppendAudit(ctx, domain.AuditRecord{
		ActorUserID: actor, Action: action, EntityType: entityType,
		EntityID: entityID, NewState: details,
	}); err != nil {
		s.logger.Error("audit write failed", slog.String("action", action), slog.String("error", err.Error()))
	}
}
