-- +goose Up
-- The pipelines default model (API 1.74, plan 33): what a write, decide or agent step's Runs on · Auto runs on. One row,
-- no owner, like session_names; absent: local (as before 1.74).
CREATE TABLE pipeline_model (
    one        boolean PRIMARY KEY DEFAULT true CHECK (one),
    mode       text NOT NULL,                              -- local | remote | assistant (Same as Assistant)
    provider   text,                                       -- remote: a providers.name
    model      text,                                       -- remote: one of its models
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE pipeline_model;
