# SQLite migrations
The [schema and migrations](../internal/core/schema.sql) are embedded in Core and applied transactionally at startup. The `migrations` table records applied versions. Add new upgrade statements with a new version to preserve existing databases. Core enables WAL, foreign keys, and a busy timeout when opening SQLite.
