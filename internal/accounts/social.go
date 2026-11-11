package accounts

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"wishlist/internal/photos"
	"wishlist/internal/problem"
)

var errUserNotFound = problem.New(http.StatusNotFound, "Пользователь не найден")
var errAddressNotFound = problem.New(http.StatusNotFound, "Адрес не найден")

type Person struct {
	ID          int64   `json:"id,string"`
	DisplayName string  `json:"display_name"`
	AvatarURL   *string `json:"avatar_url"`
}

type PublicProfile struct {
	Person
	Clothing    *string   `json:"clothing_size"`
	ShoeEU      *string   `json:"shoe_size_eu"`
	Age         *int      `json:"age"`
	Description *string   `json:"description"`
	IsFollowing bool      `json:"is_following"`
	IsSelf      bool      `json:"is_self"`
	Addresses   []Address `json:"addresses,omitempty"`
}

func ReadPublicProfile(ctx context.Context, tx pgx.Tx, viewerID, targetID int64) (PublicProfile, error) {
	profile := PublicProfile{IsSelf: viewerID == targetID}
	var avatar *string
	var mutual bool
	err := tx.QueryRow(ctx, `
		SELECT u.id, u.display_name, u.avatar_id, u.clothing_size, u.shoe_size_eu::text, u.description,
		       CASE WHEN u.birth_date <= today THEN EXTRACT(YEAR FROM age(today, u.birth_date))::int END,
		       EXISTS (SELECT 1 FROM follows WHERE follower_id = $1 AND followed_id = u.id),
		       EXISTS (SELECT 1 FROM follows WHERE follower_id = u.id AND followed_id = $1)
		FROM users u CROSS JOIN (SELECT (transaction_timestamp() AT TIME ZONE 'UTC')::date AS today) calendar
		WHERE u.id = $2
	`, viewerID, targetID).Scan(&profile.ID, &profile.DisplayName, &avatar, &profile.Clothing, &profile.ShoeEU,
		&profile.Description, &profile.Age, &profile.IsFollowing, &mutual)
	if errors.Is(err, pgx.ErrNoRows) {
		return profile, errUserNotFound
	}
	if err != nil {
		return profile, err
	}
	profile.AvatarURL = avatarURL(profile.ID, avatar)
	if profile.IsSelf || (profile.IsFollowing && mutual) {
		profile.Addresses, err = listAddresses(ctx, tx, targetID)
	}
	return profile, err
}

func (s Store) Follow(ctx context.Context, userID, targetID int64) error {
	if userID == targetID {
		return problem.New(http.StatusBadRequest, "Нельзя подписаться на себя")
	}
	tag, err := s.DB.Exec(ctx, `
		INSERT INTO follows (follower_id, followed_id)
		SELECT $1, id FROM users WHERE id = $2
		ON CONFLICT DO NOTHING
	`, userID, targetID)
	if err != nil || tag.RowsAffected() != 0 {
		return err
	}
	var exists bool
	err = s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, targetID).Scan(&exists)
	if err == nil && !exists {
		return errUserNotFound
	}
	return err
}

func (s Store) Unfollow(ctx context.Context, userID, targetID int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM follows WHERE follower_id = $1 AND followed_id = $2`, userID, targetID)
	return err
}

func (s Store) Connections(ctx context.Context, userID int64, followers bool, after int64, limit int) ([]Person, error) {
	relation := `f.follower_id = $1 AND f.followed_id = u.id`
	if followers {
		relation = `f.followed_id = $1 AND f.follower_id = u.id`
	}
	rows, err := s.DB.Query(ctx, `
		SELECT u.id, u.display_name, u.avatar_id FROM follows f JOIN users u ON `+relation+`
		WHERE u.id > $2 ORDER BY u.id LIMIT $3
	`, userID, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Person, error) {
		var p Person
		var avatar *string
		err := row.Scan(&p.ID, &p.DisplayName, &avatar)
		p.AvatarURL = avatarURL(p.ID, avatar)
		return p, err
	})
}

type Address struct {
	ID      int64  `json:"id,string"`
	Label   string `json:"label"`
	Address string `json:"address"`
}

func (s Store) Addresses(ctx context.Context, userID int64) ([]Address, error) {
	return listAddresses(ctx, s.DB, userID)
}

func listAddresses(ctx context.Context, db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, userID int64) ([]Address, error) {
	rows, err := db.Query(ctx, `SELECT id, label, address FROM delivery_addresses WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Address])
}

func (s Store) SaveAddress(ctx context.Context, userID, id int64, input Address) (Address, error) {
	input.Label = strings.TrimSpace(input.Label)
	input.Address = strings.TrimSpace(input.Address)
	if input.Label == "" || utf8.RuneCountInString(input.Label) > 80 || input.Address == "" || utf8.RuneCountInString(input.Address) > 500 ||
		strings.ContainsRune(input.Label, 0) || strings.ContainsRune(input.Address, 0) {
		return Address{}, problem.New(http.StatusBadRequest, "Укажите название до 80 символов и адрес до 500 символов")
	}
	if id != 0 {
		err := s.DB.QueryRow(ctx, `
			UPDATE delivery_addresses SET label = $3, address = $4 WHERE user_id = $1 AND id = $2 RETURNING id
		`, userID, id, input.Label, input.Address).Scan(&input.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = errAddressNotFound
		}
		return input, err
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var locked int64
		if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID).Scan(&locked); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM delivery_addresses WHERE user_id = $1`, userID).Scan(&count); err != nil {
			return err
		}
		if count >= 10 {
			return problem.New(http.StatusConflict, "Можно сохранить до 10 адресов доставки")
		}
		return tx.QueryRow(ctx, `INSERT INTO delivery_addresses (user_id, label, address) VALUES ($1, $2, $3) RETURNING id`,
			userID, input.Label, input.Address).Scan(&input.ID)
	})
	return input, err
}

func (s Store) DeleteAddress(ctx context.Context, userID, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM delivery_addresses WHERE user_id = $1 AND id = $2`, userID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return errAddressNotFound
	}
	return err
}

func avatarURL(userID int64, id *string) *string {
	if id == nil {
		return nil
	}
	url := "/api/v1/users/" + strconv.FormatInt(userID, 10) + "/avatar/" + *id
	return &url
}

func (s Store) HasAvatar(ctx context.Context, userID int64, id string) (bool, error) {
	var exists bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND avatar_id = $2)`, userID, id).Scan(&exists)
	return exists, err
}

func (s Store) SetAvatar(ctx context.Context, userID int64, id string, upload photos.Upload) error {
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := upload(ctx, tx); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE users SET avatar_id = $2 WHERE id = $1`, userID, id)
		return err
	})
}

func (s Store) DeleteAvatar(ctx context.Context, userID int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET avatar_id = NULL WHERE id = $1`, userID)
	return err
}
