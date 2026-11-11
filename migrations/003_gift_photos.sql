BEGIN;

ALTER TABLE gifts ADD COLUMN photo_ids text[] NOT NULL DEFAULT '{}'
    CHECK (cardinality(photo_ids) <= 5);

CREATE INDEX gifts_photo_ids_idx ON gifts USING gin (photo_ids);

CREATE TABLE gift_photo_objects (
    id text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX gift_photo_objects_created_idx ON gift_photo_objects (created_at, id);

COMMIT;
