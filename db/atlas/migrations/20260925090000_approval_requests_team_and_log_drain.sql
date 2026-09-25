-- Two branches each rewrote approval_requests_resource_type_check (one added 'team', the other
-- 'log_drain'); whichever ran last dropped the other's value. This restores both.
ALTER TABLE approval_requests DROP CONSTRAINT approval_requests_resource_type_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_resource_type_check
  CHECK (resource_type = ANY (ARRAY['app'::text, 'server'::text, 'database'::text, 'domain'::text, 'data_service'::text, 'backup'::text, 'invitation'::text, 'member'::text, 'team'::text, 'log_drain'::text]));
