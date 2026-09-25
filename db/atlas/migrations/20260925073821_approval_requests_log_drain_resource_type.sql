-- log_drain_delete (H8) is a destructive MCP tool (confirm = the drain's name) whose approval
-- request, like any other, is filed against approval_requests.resource_type.
ALTER TABLE approval_requests DROP CONSTRAINT approval_requests_resource_type_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_resource_type_check
  CHECK (resource_type = ANY (ARRAY['app'::text, 'server'::text, 'database'::text, 'domain'::text, 'data_service'::text, 'backup'::text, 'invitation'::text, 'member'::text, 'log_drain'::text]));
