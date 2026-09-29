-- Modify "domains" table
ALTER TABLE "public"."domains" DROP CONSTRAINT "domains_hostname_check", ADD CONSTRAINT "domains_hostname_check" CHECK ((hostname = lower(hostname)) AND (length(hostname) <= 253) AND (hostname ~ '^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'::text) AND ((hostname !~~ '*.%'::text) OR (tls <> 'auto'::text)));
