package databases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"gopanel/internal/auth"
	"gopanel/internal/model"
	"gopanel/internal/store"
	"gopanel/internal/sys"
	"gopanel/internal/validate"
)

var (
	ErrNotFound     = errors.New("database not found")
	ErrNameTaken    = errors.New("database already exists")
	ErrUserTaken    = errors.New("database user already exists")
	ErrProtected    = errors.New("that database user is protected")
	ErrNoCredential = errors.New("this database has no panel-managed user")
)

type Service struct {
	st     *store.Store
	sys    sys.System
	auth   *auth.Service
	logger *slog.Logger
}

type Item struct {
	model.Database
	SizeBytes int64    `json:"size_bytes"`
	Tables    int      `json:"tables"`
	Present   bool     `json:"present"`
	Users     []string `json:"users,omitempty"`
}

type CreateInput struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type Created struct {
	Database Item   `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func New(st *store.Store, system sys.System, authService *auth.Service, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{st: st, sys: system, auth: authService, logger: logger}
}

func (s *Service) List(ctx context.Context) ([]Item, error) {
	dbs, err := s.sys.MySQLListDatabases(ctx)
	if err != nil {
		return nil, err
	}
	records, err := s.st.ListDatabases(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]model.Database{}
	for _, rec := range records {
		byName[rec.Name] = rec
	}
	out := make([]Item, 0, len(dbs))
	seen := map[string]bool{}
	for _, info := range dbs {
		item := Item{SizeBytes: info.SizeBytes, Tables: info.Tables, Present: true}
		if rec, ok := byName[info.Name]; ok {
			item.Database = rec
			item.Users = []string{rec.Username}
			seen[info.Name] = true
		} else {
			item.Name = info.Name
		}
		out = append(out, item)
	}
	for _, rec := range records {
		if seen[rec.Name] {
			continue
		}
		out = append(out, Item{Database: rec, Users: []string{rec.Username}})
	}
	return out, nil
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*Created, error) {
	name := strings.TrimSpace(in.Name)
	user := strings.TrimSpace(in.Username)
	if err := validate.DatabaseName(name); err != nil {
		return nil, err
	}
	if user == "" {
		user = deriveUser(name)
	}
	if err := validate.DatabaseUser(user); err != nil {
		return nil, err
	}
	password := in.Password
	if password == "" {
		password = auth.GeneratePassword(24)
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}

	dbs, err := s.sys.MySQLListDatabases(ctx)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	for _, db := range dbs {
		if db.Name == name {
			return nil, ErrNameTaken
		}
	}
	users, err := s.sys.MySQLListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list database users: %w", err)
	}
	for _, u := range users {
		if u.User == user {
			return nil, ErrUserTaken
		}
	}

	if err := s.sys.MySQLCreateDatabase(ctx, name); err != nil {
		return nil, err
	}
	if err := s.sys.MySQLCreateDatabaseUser(ctx, user, password, name); err != nil {
		_ = s.sys.MySQLDropDatabase(ctx, name)
		return nil, err
	}
	enc, err := s.auth.Encrypt([]byte(password))
	if err != nil {
		_ = s.sys.MySQLDropDatabase(ctx, name)
		_ = s.sys.MySQLDropUser(ctx, user)
		return nil, err
	}
	rec := &model.Database{Name: name, Username: user, PasswordEnc: enc}
	if err := s.st.CreateDatabaseRecord(ctx, rec); err != nil {
		_ = s.sys.MySQLDropDatabase(ctx, name)
		_ = s.sys.MySQLDropUser(ctx, user)
		return nil, err
	}
	return &Created{
		Database: Item{Database: *rec, Present: true, Users: []string{user}},
		Username: user,
		Password: password,
	}, nil
}

func (s *Service) Delete(ctx context.Context, name string) error {
	if err := validate.DatabaseName(name); err != nil {
		return err
	}
	rec, err := s.st.GetDatabase(ctx, name)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := s.sys.MySQLDropDatabase(ctx, name); err != nil {
		return err
	}
	if rec != nil {
		if err := s.sys.MySQLDropUser(ctx, rec.Username); err != nil {
			s.logger.Warn("drop database user", "user", rec.Username, "error", err)
		}
		if err := s.st.DeleteDatabaseRecord(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Users(ctx context.Context) ([]sys.DBUser, error) {
	return s.sys.MySQLListUsers(ctx)
}

type UserInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Database string `json:"database"`
}

type CreatedUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Database string `json:"database"`
}

func (s *Service) CreateUser(ctx context.Context, in UserInput) (*CreatedUser, error) {
	user := strings.TrimSpace(in.Username)
	if err := validate.DatabaseUser(user); err != nil {
		return nil, err
	}
	if err := validate.DatabaseName(in.Database); err != nil {
		return nil, err
	}
	password := in.Password
	if password == "" {
		password = auth.GeneratePassword(24)
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	if isProtectedUser(user) {
		return nil, ErrProtected
	}
	users, err := s.sys.MySQLListUsers(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.User == user {
			return nil, ErrUserTaken
		}
	}
	if err := s.sys.MySQLCreateDatabaseUser(ctx, user, password, in.Database); err != nil {
		return nil, err
	}
	return &CreatedUser{Username: user, Password: password, Database: in.Database}, nil
}

func (s *Service) DeleteUser(ctx context.Context, user string) error {
	if err := validate.DatabaseUser(user); err != nil {
		return err
	}
	if isProtectedUser(user) {
		return ErrProtected
	}
	return s.sys.MySQLDropUser(ctx, user)
}

func (s *Service) SetPassword(ctx context.Context, name, password string) (string, error) {
	if err := validate.DatabaseName(name); err != nil {
		return "", err
	}
	if password == "" {
		password = auth.GeneratePassword(24)
	}
	if err := validatePassword(password); err != nil {
		return "", err
	}
	rec, err := s.st.GetDatabase(ctx, name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", ErrNoCredential
		}
		return "", err
	}
	if rec.Username == "" {
		return "", ErrNoCredential
	}
	if err := s.sys.MySQLSetPassword(ctx, rec.Username, password); err != nil {
		return "", err
	}
	enc, err := s.auth.Encrypt([]byte(password))
	if err != nil {
		return "", err
	}
	if err := s.st.UpdateDatabasePassword(ctx, name, enc); err != nil {
		return "", err
	}
	return password, nil
}

func deriveUser(name string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		default:
			return '_'
		}
	}, name)
	if len(out) > 32 {
		out = out[:32]
	}
	if out == "" {
		out = "gopanel"
	}
	return out
}

func validatePassword(password string) error {
	if len(password) < 12 || len(password) > 128 {
		return fmt.Errorf("password must be between 12 and 128 characters")
	}
	if strings.ContainsAny(password, "\x00\n\r'\"\\") {
		return fmt.Errorf("password contains unsupported characters")
	}
	return nil
}

func isProtectedUser(user string) bool {
	lower := strings.ToLower(user)
	switch lower {
	case "root", "mysql.sys", "mysql.session", "debian-sys-maint", "admin":
		return true
	}
	return strings.HasPrefix(lower, "mysql.")
}
