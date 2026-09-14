-- Cloudy: cross-table foreign keys (hand-written schema fragment).
-- Atlas reads this directory in file-name order, and togo names fragments after their
-- resource, so a foreign key declared in invitation.sql would be read before team.sql
-- creates teams. Keeping every cross-table reference here — sorted last — avoids that.
ALTER TABLE team_members ADD CONSTRAINT team_members_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE invitations ADD CONSTRAINT invitations_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE mcp_tokens ADD CONSTRAINT mcp_tokens_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE report_comments ADD CONSTRAINT report_comments_report_fk FOREIGN KEY (report_id) REFERENCES reports (id) ON DELETE CASCADE;
