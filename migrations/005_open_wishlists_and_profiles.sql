BEGIN;

ALTER TABLE wishlists
    ALTER COLUMN holiday_date DROP NOT NULL,
    ALTER COLUMN time_zone DROP NOT NULL,
    ALTER COLUMN close_at DROP NOT NULL,
    ADD CONSTRAINT wishlists_schedule_complete CHECK (num_nonnulls(holiday_date, time_zone, close_at) IN (0, 3));

DROP INDEX wishlists_owner_page_idx;
CREATE INDEX wishlists_owner_page_idx
    ON wishlists (owner_id, (archived_at IS NOT NULL), (COALESCE(holiday_date, '-infinity'::date)), id);
DROP INDEX wishlists_archive_page_idx;
CREATE INDEX wishlists_archive_page_idx
    ON wishlists (close_at, id) WHERE archived_at IS NULL AND close_at IS NOT NULL;

CREATE TYPE gift_priority AS ENUM ('high', 'normal', 'low');
CREATE COLLATION wishlist_gift_name (provider = icu, locale = 'ru-u-ks-level2', deterministic = false);

ALTER TABLE gifts
    ADD COLUMN price numeric(12,2) CHECK (price >= 0 AND price <> 'NaN'::numeric),
    ADD COLUMN priority gift_priority NOT NULL DEFAULT 'normal';
CREATE INDEX gifts_priority_page_idx ON gifts (wishlist_id, priority, id);
CREATE INDEX gifts_name_page_idx ON gifts (wishlist_id, title COLLATE wishlist_gift_name, id);
CREATE INDEX gifts_added_page_idx ON gifts (wishlist_id, created_at DESC, id DESC);
DROP INDEX gifts_wishlist_page_idx;

ALTER TABLE users
    ADD COLUMN description text CHECK (char_length(description) <= 1000),
    ADD COLUMN avatar_id text REFERENCES gift_photo_objects (id);
CREATE INDEX users_avatar_idx ON users (avatar_id) WHERE avatar_id IS NOT NULL;

CREATE TABLE follows (
    follower_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    followed_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (follower_id, followed_id),
    CHECK (follower_id <> followed_id)
);
CREATE INDEX follows_followed_idx ON follows (followed_id, follower_id);

CREATE TABLE delivery_addresses (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    label text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 80),
    address text NOT NULL CHECK (char_length(address) BETWEEN 1 AND 500)
);
CREATE INDEX delivery_addresses_user_idx ON delivery_addresses (user_id, id);

COMMIT;
