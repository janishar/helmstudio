-- +goose Up
-- Image inputs (API 1.4): a take's content hash (the md5 hex of its object, which is the S3 ETag of a single-part
-- PUT) for cmd.run.execute's image etag, set when an upload completes and lazily, on first use as a reference, for a
-- run output; and the input order of lineage edges.
ALTER TABLE takes ADD COLUMN etag text;
ALTER TABLE take_parents ADD COLUMN idx int NOT NULL DEFAULT 0; -- the parent's place in the run's image inputs
CREATE INDEX take_parents_parent ON take_parents (parent_id);  -- children ("Used in")

-- +goose Down
DROP INDEX take_parents_parent;
ALTER TABLE take_parents DROP COLUMN idx;
ALTER TABLE takes DROP COLUMN etag;
