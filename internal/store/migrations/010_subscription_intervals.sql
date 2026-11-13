ALTER TABLE subscriptions ADD COLUMN interval_minutes INTEGER NOT NULL DEFAULT 60 CHECK(interval_minutes BETWEEN 5 AND 10080);
