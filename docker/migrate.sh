#!/bin/sh
set -eu
export LC_ALL=C

script=$(mktemp)
trap 'rm "$script"' EXIT

cat > "$script" <<'SQL'
SELECT pg_advisory_lock(2079153100);
CREATE TABLE IF NOT EXISTS wishlist_schema_migrations (name text PRIMARY KEY);
SQL

for migration in /migrations/*.sql; do
    name=${migration##*/}
    case "$name" in
        *[!a-zA-Z0-9_.-]*) echo "Invalid migration filename: $name" >&2; exit 1 ;;
    esac
    if [ "$(head -n 1 "$migration")" != 'BEGIN;' ] || [ "$(tail -n 1 "$migration")" != 'COMMIT;' ]; then
        echo "$name must start with BEGIN; and end with COMMIT;" >&2
        exit 1
    fi
    {
        printf '\\set migration %s\n' "$name"
        cat <<'SQL'
SELECT NOT EXISTS (
    SELECT 1 FROM wishlist_schema_migrations WHERE name = :'migration'
) AS apply_migration \gset
\if :apply_migration
\echo Applying :migration
SQL
        # Record the migration before its COMMIT so both changes succeed together.
        sed '$d' "$migration"
        cat <<'SQL'
INSERT INTO wishlist_schema_migrations (name) VALUES (:'migration');
COMMIT;
\endif
SQL
    } >> "$script"
done

psql -X -v ON_ERROR_STOP=1 -f "$script"

# Reapply grants after migrations, including upgrades of existing volumes.
psql -X -v ON_ERROR_STOP=1 -f /roles.sql
