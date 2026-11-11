BEGIN;

CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email text NOT NULL UNIQUE,
    display_name text NOT NULL,
    password_hash text NOT NULL,
    clothing_size text,
    shoe_size_eu numeric(4,1),
    birth_date date,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL
);

CREATE TABLE wishlists (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_id bigint NOT NULL REFERENCES users (id),
    title text NOT NULL,
    holiday_date date NOT NULL,
    time_zone text NOT NULL,
    share_token text NOT NULL UNIQUE,
    close_at timestamptz NOT NULL,
    archived_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE gifts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    wishlist_id bigint NOT NULL REFERENCES wishlists (id),
    title text NOT NULL,
    description text,
    product_url text,
    active boolean NOT NULL DEFAULT true,
    gifted boolean,
    visible_in_archive boolean,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE reservations (
    gift_id bigint PRIMARY KEY REFERENCES gifts (id),
    user_id bigint NOT NULL REFERENCES users (id),
    reserved_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE gift_history (
    gift_id bigint PRIMARY KEY REFERENCES gifts (id),
    user_id bigint NOT NULL REFERENCES users (id),
    reserved_at timestamptz NOT NULL
);

CREATE INDEX wishlists_owner_idx ON wishlists (owner_id);
CREATE INDEX gifts_wishlist_idx ON gifts (wishlist_id);
CREATE INDEX reservations_user_idx ON reservations (user_id);
CREATE INDEX gift_history_user_idx ON gift_history (user_id);
CREATE INDEX wishlists_archive_due_idx ON wishlists (close_at) WHERE archived_at IS NULL;

COMMIT;
