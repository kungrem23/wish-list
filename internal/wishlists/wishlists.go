package wishlists

import (
	"context"
	"errors"
	"net/http"
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
	Title       string `json:"title"`
	HolidayDate string `json:"holiday_date,omitempty"`
	TimeZone    string `json:"time_zone,omitempty"`
}

type Summary struct {
	ID int64 `json:"id,string"`
	Details
	ShareToken string     `json:"share_token,omitempty"`
	CloseAt    *time.Time `json:"close_at"`
	ArchivedAt *time.Time `json:"archived_at"`
	Phase      string     `json:"phase"`
	GiftCount  int        `json:"gift_count"`
	AvatarID   *string    `json:"-"`
	AvatarURL  *string    `json:"avatar_url"`
}

type list struct {
	Summary
	ownerID int64
	date    *time.Time
}

var (
	errNotFound  = problem.New(http.StatusNotFound, "Список или подарок не найден")
	errListBusy  = problem.New(http.StatusServiceUnavailable, "Список временно занят. Повторите попытку")
	errEditing   = problem.New(http.StatusConflict, "Редактирование закрыто с начала дня праздника")
	errClosed    = problem.New(http.StatusConflict, "Бронирование и отмена закрыты")
	errArchive   = problem.New(http.StatusConflict, "Дождитесь архивации списка")
	errListQuota = problem.New(http.StatusConflict, "Достигнут лимит в 50 списков, включая архив")
)

const maxListsPerOwner = 50

const listColumns = `id, owner_id, title, holiday_date, time_zone, share_token, close_at, archived_at, avatar_id`

func (l *list) scan(row pgx.Row, extra ...any) error {
	var zone *string
	fields := []any{&l.ID, &l.ownerID, &l.Title, &l.date, &zone, &l.ShareToken, &l.CloseAt, &l.ArchivedAt, &l.AvatarID}
	if err := row.Scan(append(fields, extra...)...); err != nil {
		return err
	}
	if l.date != nil {
		l.HolidayDate = l.date.Format(time.DateOnly)
		l.TimeZone = *zone
		utc := l.CloseAt.UTC()
		l.CloseAt = &utc
	}
	return nil
}

func (l list) phase(now time.Time) (string, error) {
	switch {
	case l.ArchivedAt != nil:
		return "archived", nil
	case !l.open(now):
		return "awaiting_archive", nil
	}
	if l.date == nil {
		return "editing", nil
	}
	loc, err := time.LoadLocation(l.TimeZone)
	if err != nil {
		return "", err
	}
	if now.Before(platform.DayStart(*l.date, loc)) {
		return "editing", nil
	}
	return "holiday", nil
}

func (l list) open(now time.Time) bool {
	return l.ArchivedAt == nil && (l.CloseAt == nil || now.Before(*l.CloseAt))
}

func (l list) requireEditing(now time.Time) error {
	phase, err := l.phase(now)
	if err != nil {
		return err
	}
	if phase != "editing" {
		return errEditing
	}
	return nil
}

func (d Details) validate(now time.Time) (Details, *time.Time, error) {
	d.Title = strings.TrimSpace(d.Title)
	d.TimeZone = strings.TrimSpace(d.TimeZone)
	if d.Title == "" || utf8.RuneCountInString(d.Title) > 150 {
		return d, nil, problem.New(http.StatusBadRequest, "Название списка должно содержать от 1 до 150 символов")
	}
	if d.HolidayDate == "" {
		d.TimeZone = ""
		return d, nil, nil
	}
	date, loc, start, closeAt, err := platform.CalendarDay(d.HolidayDate, d.TimeZone)
	if err != nil {
		return d, nil, err
	}
	if !now.Before(start) {
		return d, nil, problem.New(http.StatusBadRequest, "Выберите будущую дату праздника в указанном часовом поясе")
	}
	year, month, day := now.In(loc).Date()
	limit := time.Date(year+1, month, day, 0, 0, 0, 0, time.UTC)
	if limit.Month() != month {
		limit = limit.AddDate(0, 0, -1)
	}
	if date.After(limit) {
		return d, nil, problem.New(http.StatusBadRequest, "Выберите дату праздника не дальше чем на год вперёд")
	}
	return d, &closeAt, nil
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func dbTime(ctx context.Context, db queryRower) (time.Time, error) {
	var now time.Time
	err := db.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, err
}

func CreateInTx(ctx context.Context, tx pgx.Tx, ownerID int64, input Details) (Summary, error) {
	// Serialize quota checks for all creates and copies by this owner.
	var id int64
	err := tx.QueryRow(ctx, "SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE", ownerID).Scan(&id)
	if err != nil {
		return Summary{}, err
	}
	var full bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wishlists WHERE owner_id = $1 OFFSET $2)`,
		ownerID, maxListsPerOwner-1).Scan(&full)
	if err != nil {
		return Summary{}, err
	}
	if full {
		return Summary{}, errListQuota
	}
	now, err := dbTime(ctx, tx)
	if err != nil {
		return Summary{}, err
	}
	input, closeAt, err := input.validate(now)
	if err != nil {
		return Summary{}, err
	}
	var l list
	err = l.scan(tx.QueryRow(ctx, `
		INSERT INTO wishlists (owner_id, title, holiday_date, time_zone, share_token, close_at)
		VALUES ($1, $2, NULLIF($3, '')::date, NULLIF($4, ''), $5, $6) RETURNING `+listColumns,
		ownerID, input.Title, input.HolidayDate, input.TimeZone, platform.Token(), closeAt))
	l.Phase = "editing"
	return l.Summary, err
}

func (s Store) Create(ctx context.Context, ownerID int64, input Details) (Summary, error) {
	var result Summary
	err := s.writeTx(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = CreateInTx(ctx, tx, ownerID, input)
		return err
	})
	return result, err
}

func (s Store) Update(ctx context.Context, ownerID, id int64, input Details) error {
	return s.withOwnedListLocked(ctx, ownerID, id, func(tx pgx.Tx, l list, now time.Time) error {
		if err := l.requireEditing(now); err != nil {
			return err
		}
		input, closeAt, err := input.validate(now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE wishlists SET title = $2, holiday_date = NULLIF($3, '')::date, time_zone = NULLIF($4, ''), close_at = $5
			WHERE id = $1
		`, l.ID, input.Title, input.HolidayDate, input.TimeZone, closeAt)
		return err
	})
}

func (s Store) withOwnedListLocked(ctx context.Context, ownerID, id int64, fn func(pgx.Tx, list, time.Time) error) error {
	return s.withLocked(ctx, "id = $1 AND owner_id = $2", []any{id, ownerID}, fn)
}

// All writes that depend on an existing list's lifecycle serialize on its row.
// The predicate comes from the caller's SQL, and request values are parameters.
func (s Store) withLocked(ctx context.Context, predicate string, args []any, fn func(pgx.Tx, list, time.Time) error) error {
	return s.writeTx(ctx, func(tx pgx.Tx) error {
		l, err := lockList(ctx, tx, predicate, args...)
		if err != nil {
			return err
		}
		// Read the clock after any wait for the lock.
		now, err := dbTime(ctx, tx)
		if err != nil {
			return err
		}
		return fn(tx, l, now)
	})
}

func lockList(ctx context.Context, tx pgx.Tx, predicate string, args ...any) (list, error) {
	var l list
	err := l.scan(tx.QueryRow(ctx, `SELECT `+listColumns+` FROM wishlists WHERE `+predicate+` FOR UPDATE`, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		err = errNotFound
	}
	return l, err
}

func (s Store) writeTx(ctx context.Context, fn func(pgx.Tx) error) error {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '3s'"); err != nil {
			return err
		}
		return fn(tx)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
		return errListBusy
	}
	return err
}

func (s Store) CopyUngifted(ctx context.Context, ownerID, id int64, input Details) (Summary, error) {
	var result Summary
	err := s.withOwnedListLocked(ctx, ownerID, id, func(tx pgx.Tx, l list, _ time.Time) error {
		if l.ArchivedAt == nil {
			return errArchive
		}
		var tooMany bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM gifts
			WHERE wishlist_id = $1 AND visible_in_archive AND gifted = false OFFSET $2)`,
			l.ID, maxGiftsPerList).Scan(&tooMany)
		if err != nil {
			return err
		}
		if tooMany {
			return errGiftQuota
		}
		result, err = CreateInTx(ctx, tx, ownerID, input)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO gifts (wishlist_id, title, description, product_url, photo_ids, price, priority)
			SELECT $2, title, description, product_url, photo_ids, price, priority FROM gifts
			WHERE wishlist_id = $1 AND visible_in_archive AND gifted = false
			ORDER BY id
		`, l.ID, result.ID)
		result.GiftCount = int(tag.RowsAffected())
		return err
	})
	return result, err
}
