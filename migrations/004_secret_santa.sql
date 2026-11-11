BEGIN;

CREATE TABLE santa_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organizer_id bigint NOT NULL REFERENCES users (id),
    title text NOT NULL,
    exchange_date date NOT NULL,
    time_zone text NOT NULL,
    close_at timestamptz NOT NULL,
    budget numeric(12,2) NOT NULL CHECK (budget >= 0),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    rules text NOT NULL,
    invite_hash bytea NOT NULL UNIQUE,
    round integer NOT NULL DEFAULT 0 CHECK (round >= 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (id, round)
);

CREATE TABLE santa_applications (
    event_id bigint NOT NULL REFERENCES santa_events (id),
    user_id bigint NOT NULL REFERENCES users (id),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'removed')),
    PRIMARY KEY (event_id, user_id)
);

ALTER TABLE wishlists ADD CONSTRAINT wishlists_id_owner_unique UNIQUE (id, owner_id);

CREATE TABLE santa_participants (
    event_id bigint NOT NULL,
    user_id bigint NOT NULL,
    wishlist_id bigint,
    PRIMARY KEY (event_id, user_id),
    FOREIGN KEY (event_id, user_id) REFERENCES santa_applications (event_id, user_id),
    FOREIGN KEY (wishlist_id, user_id) REFERENCES wishlists (id, owner_id)
);

CREATE TABLE santa_assignments (
    event_id bigint NOT NULL,
    round integer NOT NULL CHECK (round > 0),
    giver_id bigint NOT NULL,
    recipient_id bigint NOT NULL,
    PRIMARY KEY (event_id, round, giver_id),
    UNIQUE (event_id, round, recipient_id),
    FOREIGN KEY (event_id, round) REFERENCES santa_events (id, round),
    FOREIGN KEY (event_id, giver_id) REFERENCES santa_participants (event_id, user_id),
    FOREIGN KEY (event_id, recipient_id) REFERENCES santa_participants (event_id, user_id),
    CHECK (giver_id <> recipient_id)
);

CREATE INDEX santa_events_organizer_idx ON santa_events (organizer_id);
CREATE INDEX santa_applications_user_idx ON santa_applications (user_id, event_id);

ALTER TABLE reservations ADD COLUMN via_santa boolean NOT NULL DEFAULT false;

COMMIT;
