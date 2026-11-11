package santa

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"wishlist/internal/platform"
	"wishlist/internal/problem"
)

type Store struct {
	DB *pgxpool.Pool
}

type Details struct {
	Title        string `json:"title"`
	ExchangeDate string `json:"exchange_date"`
	TimeZone     string `json:"time_zone"`
	Budget       string `json:"budget"`
	Currency     string `json:"currency"`
	Rules        string `json:"rules"`
}

type CreateInput struct {
	Details
	Participate bool `json:"participate"`
}

type InvitationLink struct {
	Token string `json:"invite_token"`
}

type Created struct {
	ID int64 `json:"id,string"`
	InvitationLink
}

type Event struct {
	ID int64 `json:"id,string"`
	Details
	CloseAt     time.Time `json:"close_at"`
	Closed      bool      `json:"closed"`
	Round       int       `json:"round"`
	IsOrganizer bool      `json:"is_organizer"`
	Status      string    `json:"status,omitempty"`
}

type Recipient struct {
	Name       string `json:"name"`
	WishlistID *int64 `json:"wishlist_id,string,omitempty"`
}

type Application struct {
	UserID int64  `json:"user_id,string"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type View struct {
	Event
	Version       int64         `json:"version,string"`
	WishlistID    *int64        `json:"wishlist_id,string,omitempty"`
	Recipient     *Recipient    `json:"recipient,omitempty"`
	Applications  []Application `json:"applications,omitempty"`
	RosterChanged bool          `json:"roster_changed,omitempty"`
}

type eventRow struct {
	id          int64
	organizerID int64
	closeAt     time.Time
	round       int
	version     int64
}

var (
	errEventQuota = problem.New(http.StatusConflict, "Достигнут лимит в 50 созданных событий, включая архив")
	errEventRate  = problem.New(http.StatusTooManyRequests, "Не больше 5 новых событий в час. Повторите позже")
	errNotFound   = problem.New(http.StatusNotFound, "Событие или приглашение не найдено")
	errClosed     = problem.New(http.StatusConflict, "Событие уже в архиве")
	errStale      = problem.New(http.StatusConflict, "Событие изменилось. Проверьте состав и попробуйте снова")
	errRoster     = problem.New(http.StatusBadRequest, "Это действие недоступно для текущего статуса участника")
	errBusy       = problem.New(http.StatusServiceUnavailable, "Событие временно занято. Повторите попытку")
	budgetRE      = regexp.MustCompile(`^[0-9]{1,10}(\.[0-9]{1,2})?$`)
	currencyRE    = regexp.MustCompile(`^[A-Z]{3}$`)
)

func inviteHash(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}

func clock(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now)
	return now, err
}

func (s Store) write(ctx context.Context, action func(pgx.Tx) error) error {
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '3s'"); err != nil {
			return err
		}
		return action(tx)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
		return errBusy
	}
	return err
}

func lockEvent(ctx context.Context, tx pgx.Tx, predicate string, key any, mode string) (eventRow, error) {
	var event eventRow
	err := tx.QueryRow(ctx, `
		SELECT id, organizer_id, close_at, round, version FROM santa_events WHERE `+predicate+` `+mode,
		key).Scan(&event.id, &event.organizerID, &event.closeAt, &event.round, &event.version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = errNotFound
	}
	return event, err
}

func (e eventRow) requireOpen(ctx context.Context, tx pgx.Tx) error {
	now, err := clock(ctx, tx)
	if err == nil && !now.Before(e.closeAt) {
		return errClosed
	}
	return err
}

func (s Store) organize(ctx context.Context, organizer, id int64, action func(pgx.Tx, eventRow) error) error {
	return s.write(ctx, func(tx pgx.Tx) error {
		e, err := lockEvent(ctx, tx, "id = $1", id, "FOR UPDATE")
		if err != nil {
			return err
		}
		if e.organizerID != organizer {
			return errNotFound
		}
		if err := e.requireOpen(ctx, tx); err != nil {
			return err
		}
		return action(tx, e)
	})
}

func (s Store) Create(ctx context.Context, organizer int64, input CreateInput) (Created, error) {
	var result Created
	input.Title = strings.TrimSpace(input.Title)
	input.TimeZone = strings.TrimSpace(input.TimeZone)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.Budget = strings.TrimSpace(input.Budget)
	input.Rules = strings.TrimSpace(input.Rules)
	if input.Title == "" || utf8.RuneCountInString(input.Title) > 150 {
		return result, problem.New(http.StatusBadRequest, "Название события должно содержать от 1 до 150 символов")
	}
	if utf8.RuneCountInString(input.Rules) > 5000 {
		return result, problem.New(http.StatusBadRequest, "Правила должны содержать не больше 5000 символов")
	}
	if !budgetRE.MatchString(input.Budget) || !currencyRE.MatchString(input.Currency) {
		return result, problem.New(http.StatusBadRequest, "Укажите сумму бюджета и трёхбуквенный код валюты")
	}
	_, _, _, closeAt, err := platform.CalendarDay(input.ExchangeDate, input.TimeZone)
	if err != nil {
		return result, err
	}
	result.Token = platform.Token()
	err = s.write(ctx, func(tx pgx.Tx) error {
		// Serialize quotas across all API processes and concurrent creates.
		var locked int64
		if err := tx.QueryRow(ctx, "SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE", organizer).Scan(&locked); err != nil {
			return err
		}
		var count, recent int
		if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE created_at > statement_timestamp() - interval '1 hour')
		    FROM santa_events WHERE organizer_id = $1`, organizer).Scan(&count, &recent); err != nil {
			return err
		}
		if count >= 50 {
			return errEventQuota
		}
		if recent >= 5 {
			return errEventRate
		}
		if input.Participate {
			var full bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM santa_applications WHERE user_id = $1 OFFSET 99)", organizer).Scan(&full); err != nil {
				return err
			}
			if full {
				return problem.New(http.StatusConflict, "Достигнут лимит участия в 100 событиях, включая архив")
			}
		}
		now, err := clock(ctx, tx)
		if err != nil {
			return err
		}
		if !now.Before(closeAt) {
			return problem.New(http.StatusBadRequest, "Выберите дату обмена, которая ещё не закончилась")
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO santa_events (organizer_id, title, exchange_date, time_zone, close_at, budget, currency, rules, invite_hash)
			VALUES ($1, $2, $3::text::date, $4, $5, $6::text::numeric, $7, $8, $9) RETURNING id
		`, organizer, input.Title, input.ExchangeDate, input.TimeZone, closeAt, input.Budget, input.Currency, input.Rules,
			inviteHash(result.Token)).Scan(&result.ID)
		if err != nil || !input.Participate {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO santa_applications (event_id, user_id, status) VALUES ($1, $2, 'approved')`, result.ID, organizer)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO santa_participants (event_id, user_id) VALUES ($1, $2)`, result.ID, organizer)
		return err
	})
	return result, err
}

func (s Store) Rotate(ctx context.Context, organizer, id int64) (InvitationLink, error) {
	result := InvitationLink{Token: platform.Token()}
	err := s.organize(ctx, organizer, id, func(tx pgx.Tx, e eventRow) error {
		_, err := tx.Exec(ctx, "UPDATE santa_events SET invite_hash = $2 WHERE id = $1", e.id, inviteHash(result.Token))
		return err
	})
	return result, err
}

const eventFields = `e.id, e.title, e.exchange_date::text, e.time_zone, e.budget::text, e.currency, e.rules,
	e.close_at, statement_timestamp() >= e.close_at, e.round, e.organizer_id = $1, COALESCE(a.status, '')`

func (e *Event) scan(row pgx.Row, extra ...any) error {
	fields := []any{&e.ID, &e.Title, &e.ExchangeDate, &e.TimeZone, &e.Budget, &e.Currency, &e.Rules,
		&e.CloseAt, &e.Closed, &e.Round, &e.IsOrganizer, &e.Status}
	return row.Scan(append(fields, extra...)...)
}

type Cursor struct {
	Closed bool
	Date   string
	ID     int64
}

func (s Store) List(ctx context.Context, userID int64, archived *bool, cursor Cursor, limit int) ([]Event, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT `+eventFields+` FROM santa_events e
		LEFT JOIN santa_applications a ON a.event_id = e.id AND a.user_id = $1
		WHERE (e.organizer_id = $1 OR a.user_id IS NOT NULL)
		  AND ($2::boolean IS NULL OR (statement_timestamp() >= e.close_at) = $2)
		  AND ($3::bigint = 0 OR (statement_timestamp() >= e.close_at, e.exchange_date, e.id) > ($4, $5::text::date, $3))
		ORDER BY statement_timestamp() >= e.close_at, e.exchange_date, e.id
		LIMIT $6
	`, userID, archived, cursor.ID, cursor.Closed, emptyDate(cursor.Date), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) {
		var e Event
		err := e.scan(row)
		return e, err
	})
}

func emptyDate(date string) string {
	if date == "" {
		return "0001-01-01"
	}
	return date
}

func (s Store) Invitation(ctx context.Context, userID int64, token string) (Event, error) {
	var e Event
	err := e.scan(s.DB.QueryRow(ctx, `
		SELECT `+eventFields+` FROM santa_events e
		LEFT JOIN santa_applications a ON a.event_id = e.id AND a.user_id = $1 WHERE e.invite_hash = $2
	`, userID, inviteHash(token)))
	if errors.Is(err, pgx.ErrNoRows) {
		err = errNotFound
	}
	return e, err
}

func (s Store) View(ctx context.Context, userID, id int64) (View, error) {
	var view View
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		err := view.scan(tx.QueryRow(ctx, `
			SELECT `+eventFields+`, e.version, p.wishlist_id
			FROM santa_events e
			LEFT JOIN santa_applications a ON a.event_id = e.id AND a.user_id = $1
			LEFT JOIN santa_participants p ON p.event_id = e.id AND p.user_id = $1
			WHERE e.id = $2 AND (e.organizer_id = $1 OR a.user_id IS NOT NULL)
		`, userID, id), &view.Version, &view.WishlistID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		if view.Round > 0 && (view.IsOrganizer || view.Status == "approved") {
			err := tx.QueryRow(ctx, `
				SELECT (SELECT count(*) FROM santa_participants WHERE event_id = $1) <>
				       (SELECT count(*) FROM santa_assignments WHERE event_id = $1 AND round = $2)
			`, id, view.Round).Scan(&view.RosterChanged)
			if err != nil {
				return err
			}
		}
		if view.Round > 0 && view.Status == "approved" {
			var recipient Recipient
			err = tx.QueryRow(ctx, `
				SELECT u.display_name, CASE WHEN NOT $4 AND w.archived_at IS NULL
					AND (w.close_at IS NULL OR w.close_at > statement_timestamp()) THEN w.id END
				FROM santa_assignments sa
				JOIN users u ON u.id = sa.recipient_id
				JOIN santa_participants p ON p.event_id = sa.event_id AND p.user_id = sa.recipient_id
				LEFT JOIN wishlists w ON w.id = p.wishlist_id
				WHERE sa.event_id = $1 AND sa.round = $2 AND sa.giver_id = $3
			`, id, view.Round, userID, view.Closed).Scan(&recipient.Name, &recipient.WishlistID)
			if err == nil {
				view.Recipient = &recipient
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if !view.IsOrganizer {
			return nil
		}
		rows, err := tx.Query(ctx, `
			SELECT a.user_id, u.display_name, a.status FROM santa_applications a JOIN users u ON u.id = a.user_id
			WHERE a.event_id = $1 ORDER BY u.display_name, u.id
		`, id)
		if err != nil {
			return err
		}
		view.Applications, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Application])
		return err
	})
	return view, err
}
