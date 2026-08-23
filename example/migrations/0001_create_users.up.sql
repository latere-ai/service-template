-- The first migration. Replace it with the schema this service owns.
--
-- Every migration is forward only and immutable once applied: the applied set
-- is tracked with the digest of this file, so editing it after it has run is
-- rejected. Write a new migration instead.

CREATE TABLE users (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email      text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX users_created_at_idx ON users (created_at);
