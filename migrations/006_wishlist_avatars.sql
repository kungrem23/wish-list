BEGIN;

ALTER TABLE wishlists ADD COLUMN avatar_id text REFERENCES gift_photo_objects (id);
CREATE INDEX wishlists_avatar_idx ON wishlists (avatar_id) WHERE avatar_id IS NOT NULL;

COMMIT;
