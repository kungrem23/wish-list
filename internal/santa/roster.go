package santa

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5"

	"wishlist/internal/problem"
)

type Decision struct {
	Version int64  `json:"version,string"`
	Status  string `json:"status"`
}

type DrawInput struct {
	Version      int64    `json:"version,string"`
	Members      []string `json:"members,omitempty"` // Accepted for clients that still send the old roster field.
	ConfirmRound *int     `json:"confirm_round,omitempty"`
}

type ConfirmationRequired struct {
	Round int
}

const ConfirmationMessage = "Как минимум один Тайный Санта уже забронировал подарок. Новая жеребьёвка может изменить получателей. Продолжить?"

func (e *ConfirmationRequired) Error() string { return ConfirmationMessage }

func (s Store) Apply(ctx context.Context, userID int64, token string) (int64, error) {
	var id int64
	err := s.write(ctx, func(tx pgx.Tx) error {
		e, err := lockEvent(ctx, tx, "invite_hash = $1", inviteHash(token), "FOR UPDATE")
		if err != nil {
			return err
		}
		if err := e.requireOpen(ctx, tx); err != nil {
			return err
		}
		id = e.id
		var existing bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM santa_applications WHERE event_id = $1 AND user_id = $2)", e.id, userID).Scan(&existing); err != nil {
			return err
		}
		if existing {
			return nil
		}
		// Lock the applicant before counting concurrent joins to different events.
		var locked int64
		if err := tx.QueryRow(ctx, "SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE", userID).Scan(&locked); err != nil {
			return err
		}
		var full bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM santa_applications WHERE user_id = $1 OFFSET 99)", userID).Scan(&full); err != nil {
			return err
		}
		if full {
			return problem.New(http.StatusConflict, "Достигнут лимит участия в 100 событиях, включая архив")
		}
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM santa_applications WHERE event_id = $1 OFFSET 199)", e.id).Scan(&full); err != nil {
			return err
		}
		if full {
			return problem.New(http.StatusConflict, "В событии уже 200 заявок")
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO santa_applications (event_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING
		`, e.id, userID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return bumpVersion(ctx, tx, e.id)
	})
	return id, err
}

func bumpVersion(ctx context.Context, tx pgx.Tx, id int64) error {
	_, err := tx.Exec(ctx, "UPDATE santa_events SET version = version + 1 WHERE id = $1", id)
	return err
}

func (s Store) Decide(ctx context.Context, organizer, id, applicant int64, input Decision) error {
	return s.organize(ctx, organizer, id, func(tx pgx.Tx, e eventRow) error {
		if e.version != input.Version {
			return errStale
		}
		var previous string
		err := tx.QueryRow(ctx, "SELECT status FROM santa_applications WHERE event_id = $1 AND user_id = $2", e.id, applicant).Scan(&previous)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		switch input.Status {
		case "approved":
			if previous == "approved" {
				return nil
			}
			_, err = tx.Exec(ctx, "INSERT INTO santa_participants (event_id, user_id) VALUES ($1, $2)", e.id, applicant)
		case "removed":
			if applicant == e.organizerID {
				return problem.New(http.StatusForbidden, "Организатор не может исключить себя")
			}
			if previous != "approved" {
				return errRoster
			}
			// Revoke both affected grants immediately. Other pairs keep their
			// assignments until the organizer runs the next draw.
			_, err = tx.Exec(ctx, `
				DELETE FROM santa_assignments WHERE event_id = $1 AND (giver_id = $2 OR recipient_id = $2)
			`, e.id, applicant)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, "DELETE FROM santa_participants WHERE event_id = $1 AND user_id = $2", e.id, applicant)
		case "rejected":
			if previous != "pending" {
				return errRoster
			}
		default:
			return errRoster
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE santa_applications SET status = $3 WHERE event_id = $1 AND user_id = $2", e.id, applicant, input.Status)
		if err != nil {
			return err
		}
		return bumpVersion(ctx, tx, e.id)
	})
}

func (s Store) Draw(ctx context.Context, organizer, id int64, input DrawInput) error {
	return s.organize(ctx, organizer, id, func(tx pgx.Tx, e eventRow) error {
		if input.Version != e.version || (input.ConfirmRound != nil && *input.ConfirmRound != e.round) {
			return errStale
		}
		rows, err := tx.Query(ctx, "SELECT user_id FROM santa_participants WHERE event_id = $1 ORDER BY user_id", e.id)
		if err != nil {
			return err
		}
		members, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if len(members) < 2 {
			return problem.New(http.StatusBadRequest, "Для жеребьёвки нужны минимум два участника")
		}
		if e.round > 0 {
			booked, err := checkBookings(ctx, tx, e)
			if err != nil {
				return err
			}
			if booked && input.ConfirmRound == nil {
				return &ConfirmationRequired{Round: e.round}
			}
		}
		recipients, err := shuffleRecipients(members)
		if err != nil {
			return err
		}
		if e.round > 0 {
			if _, err := tx.Exec(ctx, "DELETE FROM santa_assignments WHERE event_id = $1", e.id); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, "UPDATE santa_events SET round = round + 1, version = version + 1 WHERE id = $1", e.id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO santa_assignments (event_id, round, giver_id, recipient_id)
			SELECT $1, $2, pair.giver, pair.recipient FROM unnest($3::bigint[], $4::bigint[]) AS pair(giver, recipient)
		`, e.id, e.round+1, members, recipients)
		return err
	})
}

func checkBookings(ctx context.Context, tx pgx.Tx, e eventRow) (bool, error) {
	// Bookings through either route take the same giver lock. Lock in ID order
	// so overlapping events can check their rounds concurrently.
	_, err := tx.Exec(ctx, `
		SELECT u.id FROM users u JOIN santa_assignments sa ON sa.giver_id = u.id
		WHERE sa.event_id = $1 AND sa.round = $2 ORDER BY u.id FOR NO KEY UPDATE OF u
	`, e.id, e.round)
	if err != nil {
		return false, err
	}
	if err := e.requireOpen(ctx, tx); err != nil {
		return false, err
	}
	var found bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM santa_assignments sa
			JOIN reservations r ON r.user_id = sa.giver_id
			JOIN gifts g ON g.id = r.gift_id
			JOIN wishlists w ON w.id = g.wishlist_id AND w.owner_id = sa.recipient_id
			WHERE sa.event_id = $1 AND sa.round = $2
		)
	`, e.id, e.round).Scan(&found)
	return found, err
}

// Uniform shuffles followed by rejection make every valid derangement equally likely.
func shuffleRecipients(members []int64) ([]int64, error) {
	recipients := slices.Clone(members)
	for {
		for i := len(recipients) - 1; i > 0; i-- {
			j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
			if err != nil {
				return nil, err
			}
			index := j.Int64()
			recipients[i], recipients[index] = recipients[index], recipients[i]
		}
		valid := true
		for i, recipient := range recipients {
			if members[i] == recipient {
				valid = false
				break
			}
		}
		if valid {
			return recipients, nil
		}
	}
}
