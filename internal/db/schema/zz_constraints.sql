-- Hosbah: cross-table foreign keys (hand-written schema fragment).
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

-- P2: apps and deploys. Same composite (x_id, team_id) pattern: nothing an app owns can name a
-- team other than the app's. A server that still has apps cannot be deleted (RESTRICT) — the API
-- says so instead of letting a cascade take the apps with it.
ALTER TABLE apps ADD CONSTRAINT apps_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE apps ADD CONSTRAINT apps_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE RESTRICT;
ALTER TABLE process_types ADD CONSTRAINT process_types_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE env_vars ADD CONSTRAINT env_vars_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE domains ADD CONSTRAINT domains_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE deployments ADD CONSTRAINT deployments_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE deployments ADD CONSTRAINT deployments_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;
ALTER TABLE deployment_logs ADD CONSTRAINT deployment_logs_deployment_fk FOREIGN KEY (deployment_id, team_id) REFERENCES deployments (id, team_id) ON DELETE CASCADE;
ALTER TABLE deployment_logs ADD CONSTRAINT deployment_logs_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;
ALTER TABLE releases ADD CONSTRAINT releases_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE releases ADD CONSTRAINT releases_deployment_fk FOREIGN KEY (deployment_id, team_id) REFERENCES deployments (id, team_id) ON DELETE CASCADE;
ALTER TABLE notifications ADD CONSTRAINT notifications_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE operations ADD CONSTRAINT operations_deployment_fk FOREIGN KEY (deployment_id) REFERENCES deployments (id) ON DELETE SET NULL;
CREATE INDEX operations_deployment_idx ON operations (deployment_id) WHERE deployment_id IS NOT NULL;
-- P5: Caddy proxy manager.
ALTER TABLE proxy_hosts ADD CONSTRAINT proxy_hosts_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE proxy_hosts ADD CONSTRAINT proxy_hosts_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;
ALTER TABLE tunnels ADD CONSTRAINT tunnels_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
ALTER TABLE tunnels ADD CONSTRAINT tunnels_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;
ALTER TABLE tunnels ADD CONSTRAINT tunnels_credential_fk FOREIGN KEY (credential_id) REFERENCES credentials (id) ON DELETE RESTRICT;
-- Imported workloads: the tombstones of deleted imported records go with their server.
ALTER TABLE import_ignores ADD CONSTRAINT import_ignores_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;
-- Server metrics history goes with its server.
ALTER TABLE server_metrics ADD CONSTRAINT server_metrics_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;

ALTER TABLE server_logos ADD CONSTRAINT server_logos_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;
