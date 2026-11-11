BEGIN;

CREATE INDEX wishlists_owner_page_idx ON wishlists (owner_id, (archived_at IS NOT NULL), holiday_date, id);
CREATE INDEX gifts_wishlist_page_idx ON gifts (wishlist_id, id);
CREATE INDEX reservations_user_page_idx ON reservations (user_id, reserved_at DESC, gift_id DESC);
CREATE INDEX gift_history_user_page_idx ON gift_history (user_id, reserved_at DESC, gift_id DESC);
CREATE INDEX wishlists_archive_page_idx ON wishlists (close_at, id) WHERE archived_at IS NULL;

DROP INDEX wishlists_owner_idx;
DROP INDEX gifts_wishlist_idx;
DROP INDEX reservations_user_idx;
DROP INDEX gift_history_user_idx;
DROP INDEX wishlists_archive_due_idx;

COMMIT;
