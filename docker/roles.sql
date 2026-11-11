-- Provision restricted service roles after migrations, including existing volumes.
\getenv app_password WISHLIST_DB_PASSWORD
\getenv archive_password ARCHIVE_DB_PASSWORD
BEGIN;
SELECT pg_advisory_xact_lock(2079153100);
SELECT 'CREATE ROLE wishlist_app LOGIN' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'wishlist_app') \gexec
SELECT 'CREATE ROLE wishlist_archive LOGIN' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'wishlist_archive') \gexec
SELECT format('ALTER ROLE wishlist_app WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD %L', :'app_password') \gexec
SELECT format('ALTER ROLE wishlist_archive WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD %L', :'archive_password') \gexec
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO wishlist_app, wishlist_archive;
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM wishlist_app, wishlist_archive;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM wishlist_app, wishlist_archive;
GRANT SELECT, INSERT, UPDATE, DELETE ON
    users, sessions, wishlists, gifts, reservations, gift_history, gift_photo_objects,
    follows, delivery_addresses, santa_events, santa_applications, santa_participants, santa_assignments
    TO wishlist_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO wishlist_app;
GRANT SELECT ON users, wishlists, gifts, reservations, gift_history, gift_photo_objects TO wishlist_archive;
-- SELECT FOR UPDATE also requires UPDATE permission on the selected table.
GRANT UPDATE ON wishlists, gifts, gift_photo_objects TO wishlist_archive;
GRANT INSERT ON gift_history TO wishlist_archive;
GRANT DELETE ON reservations, gift_photo_objects TO wishlist_archive;
ALTER ROLE wishlist_app SET search_path = pg_catalog, public;
ALTER ROLE wishlist_archive SET search_path = pg_catalog, public;
COMMIT;
