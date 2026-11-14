-- +goose Up
-- The dev role: the dev Jobs view in the web. Admin stays the ADMIN_EMAIL account's alone (startup.EnsureAdmin).
ALTER TABLE roles DROP CONSTRAINT roles_role_check, ADD CONSTRAINT roles_role_check CHECK (role IN ('user', 'dev', 'admin'));

-- +goose Down
DELETE FROM roles WHERE role = 'dev';
ALTER TABLE roles DROP CONSTRAINT roles_role_check, ADD CONSTRAINT roles_role_check CHECK (role IN ('user', 'admin'));
