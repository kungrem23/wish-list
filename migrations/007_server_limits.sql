BEGIN;

ALTER TABLE gift_photo_objects
    ADD COLUMN owner_id bigint REFERENCES users (id),
    ADD COLUMN size_bytes bigint NOT NULL DEFAULT 5242880 CHECK (size_bytes BETWEEN 1 AND 5242880);

-- Legacy objects have no recorded size. Reserve the maximum until they are removed.
UPDATE gift_photo_objects p SET owner_id = COALESCE(
    (SELECT min(w.owner_id) FROM gifts g JOIN wishlists w ON w.id = g.wishlist_id WHERE g.photo_ids @> ARRAY[p.id]),
    (SELECT min(id) FROM users WHERE avatar_id = p.id),
    (SELECT min(owner_id) FROM wishlists WHERE avatar_id = p.id)
);
CREATE INDEX gift_photo_objects_owner_idx ON gift_photo_objects (owner_id, created_at);
CREATE INDEX santa_events_owner_created_idx ON santa_events (organizer_id, id);
ALTER TABLE santa_events ADD COLUMN created_at timestamptz NOT NULL DEFAULT clock_timestamp();

COMMIT;
