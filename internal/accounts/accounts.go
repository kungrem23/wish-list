package accounts

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"wishlist/internal/platform"
	"wishlist/internal/problem"
)

type Store struct {
	DB *pgxpool.Pool
}

type Profile struct {
	ID          int64   `json:"id,string"`
	Email       string  `json:"email"`
	DisplayName string  `json:"display_name"`
	Clothing    *string `json:"clothing_size"`
	ShoeEU      *string `json:"shoe_size_eu"`
	BirthDate   *string `json:"birth_date"`
	Description *string `json:"description"`
	AvatarURL   *string `json:"avatar_url"`
}

type ProfileInput struct {
	Clothing    *string `json:"clothing_size"`
	ShoeEU      *string `json:"shoe_size_eu"`
	BirthDate   *string `json:"birth_date"`
	Description *string `json:"description"`
}

type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type Registration struct {
	Credentials
	DisplayName string `json:"display_name"`
}

type Session struct {
	Token     string
	ExpiresAt time.Time
}

var (
	ErrSession = problem.New(http.StatusUnauthorized, "Войдите в аккаунт")
	errLogin   = problem.New(http.StatusUnauthorized, "Неверный email или пароль")
	shoeSize   = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9])?$`)
)

func (s Store) Register(ctx context.Context, input Registration) (Session, error) {
	email := NormalizeEmail(input.Email)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 {
		return Session{}, problem.New(http.StatusBadRequest, "Укажите корректный email")
	}
	name := strings.TrimSpace(input.DisplayName)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return Session{}, problem.New(http.StatusBadRequest, "Имя должно содержать от 1 до 100 символов")
	}
	if utf8.RuneCountInString(input.Password) < 8 || len(input.Password) > 72 {
		return Session{}, problem.New(http.StatusBadRequest, "Пароль должен содержать не менее 8 символов и не более 72 байт")
	}
	if _, blocked := commonPasswords[strings.ToLower(input.Password)]; blocked {
		return Session{}, problem.New(http.StatusBadRequest, "Этот пароль слишком распространён. Выберите другой")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return Session{}, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback(ctx)

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash)
		VALUES ($1, $2, $3) RETURNING id
	`, email, name, string(hash)).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Session{}, problem.New(http.StatusConflict, "Этот email уже зарегистрирован")
	}
	if err != nil {
		return Session{}, err
	}

	session, err := newSession(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	return session, tx.Commit(ctx)
}

func (s Store) Login(ctx context.Context, input Credentials) (Session, error) {
	if len(input.Password) > 72 {
		return Session{}, errLogin
	}
	var id int64
	var hash string
	err := s.DB.QueryRow(ctx, `SELECT id, password_hash FROM users WHERE email = $1`,
		NormalizeEmail(input.Email)).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		// Match the cost of a password check for an existing account.
		const dummy = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
		_ = bcrypt.CompareHashAndPassword([]byte(dummy), []byte(input.Password))
		return Session{}, errLogin
	}
	if err != nil {
		return Session{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) != nil {
		return Session{}, errLogin
	}
	return newSession(ctx, s.DB, id)
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func newSession(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64) (Session, error) {
	session := Session{Token: platform.Token()}
	err := db.QueryRow(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES ($1, $2, clock_timestamp() + interval '720 hours') RETURNING expires_at
	`, tokenHash(session.Token), id).Scan(&session.ExpiresAt)
	return session, err
}

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (s Store) UserID(ctx context.Context, token string) (int64, error) {
	if len(token) != 43 {
		return 0, ErrSession
	}
	var id int64
	err := s.DB.QueryRow(ctx, `
		SELECT user_id FROM sessions WHERE token_hash = $1 AND expires_at > clock_timestamp()
	`, tokenHash(token)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrSession
	}
	return id, err
}

func (s Store) Logout(ctx context.Context, token string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash(token))
	return err
}

const profileColumns = `id, email, display_name, clothing_size, shoe_size_eu::text, to_char(birth_date, 'YYYY-MM-DD'), description, avatar_id`

func scanProfile(row pgx.Row) (Profile, error) {
	var p Profile
	var avatar *string
	err := row.Scan(&p.ID, &p.Email, &p.DisplayName, &p.Clothing, &p.ShoeEU, &p.BirthDate, &p.Description, &avatar)
	p.AvatarURL = avatarURL(p.ID, avatar)
	return p, err
}

func (s Store) Profile(ctx context.Context, id int64) (Profile, error) {
	return scanProfile(s.DB.QueryRow(ctx, `SELECT `+profileColumns+` FROM users WHERE id = $1`, id))
}

func (s Store) UpdateProfile(ctx context.Context, id int64, input ProfileInput) (Profile, error) {
	clothing := optional(input.Clothing)
	shoes := optional(input.ShoeEU)
	birth := optional(input.BirthDate)
	description := optional(input.Description)
	if description != nil && (utf8.RuneCountInString(*description) > 1000 || strings.ContainsRune(*description, 0)) {
		return Profile{}, problem.New(http.StatusBadRequest, "Описание профиля должно содержать не более 1000 символов")
	}
	if clothing != nil && utf8.RuneCountInString(*clothing) > 40 {
		return Profile{}, problem.New(http.StatusBadRequest, "Размер одежды слишком длинный")
	}
	if shoes != nil && (!shoeSize.MatchString(*shoes) || strings.Trim(*shoes, "0.") == "") {
		return Profile{}, problem.New(http.StatusBadRequest, "Укажите европейский размер обуви с точностью до одной десятой")
	}
	if birth != nil {
		date, err := time.Parse(time.DateOnly, *birth)
		if err != nil || date.Year() < 1 {
			return Profile{}, problem.New(http.StatusBadRequest, "Укажите корректную дату рождения")
		}
		if date.After(time.Now()) {
			return Profile{}, problem.New(http.StatusBadRequest, "Дата рождения не может быть в будущем")
		}
	}
	return scanProfile(s.DB.QueryRow(ctx, `
		UPDATE users SET clothing_size = $2, shoe_size_eu = $3::text::numeric, birth_date = $4::text::date, description = $5
		WHERE id = $1 RETURNING `+profileColumns, id, clothing, shoes, birth, description))
}

func optional(value *string) *string {
	if value != nil {
		trimmed := strings.TrimSpace(*value)
		if trimmed != "" {
			return &trimmed
		}
	}
	return nil
}
