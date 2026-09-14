-- togo placeholder query so sqlc has input on a fresh project. Real per-resource
-- queries are added by `togo make:resource`. Safe to keep or remove later.
-- name: Ping :one
SELECT 1 AS ok;
