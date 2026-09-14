-- Cloudy: cross-table foreign keys (hand-written schema fragment).
-- Atlas reads this directory in file-name order, and togo names fragments after their
-- resource, so a foreign key declared in invitation.sql would be read before team.sql
-- creates teams. Keeping every cross-table reference here — sorted last — avoids that.
ALTER TABLE team_members ADD CONSTRAINT team_members_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE invitations ADD CONSTRAINT invitations_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE mcp_tokens ADD CONSTRAINT mcp_tokens_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE report_comments ADD CONSTRAINT report_comments_report_fk FOREIGN KEY (report_id) REFERENCES reports (id) ON DELETE CASCADE;

-- P1: servers, enrolment, operations, credentials. The (server_id, team_id) pairs reference
-- servers (id, team_id), so an operation, enrolment or log line can never name a team other than
-- its server's, whatever a handler passes.
ALTER TABLE credentials ADD CONSTRAINT credentials_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE servers ADD CONSTRAINT servers_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE servers ADD CONSTRAINT servers_credential_fk FOREIGN KEY (credential_id) REFERENCES credentials (id) ON DELETE SET NULL;
ALTER TABLE agent_enrollments ADD CONSTRAINT agent_enrollments_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;
ALTER TABLE operations ADD CONSTRAINT operations_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;
ALTER TABLE operation_logs ADD CONSTRAINT operation_logs_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;
ALTER TABLE operation_logs ADD CONSTRAINT operation_logs_operation_fk FOREIGN KEY (operation_id) REFERENCES operations (id) ON DELETE CASCADE;
