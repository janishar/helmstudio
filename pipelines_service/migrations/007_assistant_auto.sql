-- API 1.68 (alpha.2 item 12, Auto mode): a generation Auto mode let run by itself is a row of kind `auto`, keyed by its
-- call, with its takes and GPU seconds (the screen's estimate); the caps count the last hour's rows per user.
ALTER TABLE assistant_spend ADD COLUMN IF NOT EXISTS takes integer;
ALTER TABLE assistant_spend ADD COLUMN IF NOT EXISTS gpu_s integer;
