# SQLite migrations
V1 schema lives in `internal/core/schema.sql`, embedded in Core. It is applied transactionally at startup and tracked in `migrations`. Future migrations must get a new version and upgrade statements; do not edit historical schema for existing databases. WAL + foreign_keys + busy_timeout are enabled on every connection. Current first release has one migration.
