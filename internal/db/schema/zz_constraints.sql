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
-- An app's uploaded logo goes with its app.
ALTER TABLE app_logos ADD CONSTRAINT app_logos_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
-- Where the boxes of an app's overview canvas sit goes with the app.
ALTER TABLE app_canvas ADD CONSTRAINT app_canvas_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
-- A site's monitors go with their app; incidents with their monitor (and their app).
ALTER TABLE monitors ADD CONSTRAINT monitors_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE monitor_incidents ADD CONSTRAINT monitor_incidents_monitor_fk FOREIGN KEY (monitor_id, team_id) REFERENCES monitors (id, team_id) ON DELETE CASCADE;
ALTER TABLE monitor_incidents ADD CONSTRAINT monitor_incidents_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
-- A site's schedules go with their app; runs with their schedule (and their app). A run outlives
-- its operation's row (SET NULL): the run keeps its own copy of the outcome.
ALTER TABLE schedules ADD CONSTRAINT schedules_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE schedule_runs ADD CONSTRAINT schedule_runs_schedule_fk FOREIGN KEY (schedule_id, team_id) REFERENCES schedules (id, team_id) ON DELETE CASCADE;
ALTER TABLE schedule_runs ADD CONSTRAINT schedule_runs_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE schedule_runs ADD CONSTRAINT schedule_runs_operation_fk FOREIGN KEY (operation_id) REFERENCES operations (id) ON DELETE SET NULL;
-- A site's storage: volumes and buckets go with their app. A MinIO data service that still has app
-- buckets cannot be deleted (NO ACTION = restrict, checked at the end of the statement so deleting a
-- whole team, which removes both sides at once, still works).
ALTER TABLE app_volumes ADD CONSTRAINT app_volumes_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE app_buckets ADD CONSTRAINT app_buckets_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE app_buckets ADD CONSTRAINT app_buckets_service_fk FOREIGN KEY (service_id, team_id) REFERENCES data_services (id, team_id) ON DELETE NO ACTION;
ALTER TABLE app_buckets ADD CONSTRAINT app_buckets_sealed_check CHECK (secret_sealed LIKE 'csv1:%');
-- A site's realtime configuration goes with its app.
ALTER TABLE app_realtime ADD CONSTRAINT app_realtime_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
-- A site's browsers go with their app (the agent's app.remove takes their containers).
ALTER TABLE app_browsers ADD CONSTRAINT app_browsers_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE app_browsers ADD CONSTRAINT app_browsers_sealed_check CHECK (token_sealed LIKE 'csv1:%');
-- A site's installed recipes go with their app (the agent's app.remove takes their containers). A
-- proxy host somebody deleted by hand just stops being the install's.
ALTER TABLE app_installs ADD CONSTRAINT app_installs_app_fk FOREIGN KEY (app_id, team_id) REFERENCES apps (id, team_id) ON DELETE CASCADE;
ALTER TABLE app_installs ADD CONSTRAINT app_installs_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;
ALTER TABLE app_installs ADD CONSTRAINT app_installs_proxy_fk FOREIGN KEY (proxy_host_id) REFERENCES proxy_hosts (id) ON DELETE SET NULL;
ALTER TABLE app_installs ADD CONSTRAINT app_installs_sealed_check CHECK (secrets LIKE 'csv1:%');

-- GitHub App installations belong to a team and go with it.
ALTER TABLE github_installations ADD CONSTRAINT github_installations_team_fk FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
