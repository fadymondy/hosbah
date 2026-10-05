# Integrations API (MH-1153)

The integrations API lets an outside system (Mahaam first) connect a single Hosbah app. With that connection it can deploy the app and open its customer panel for a person. The code is in `internal/api/integrations.go` and `internal/api/oauth.go`.

## Getting an app token

You can get an app token (`hba_…`) in one of three ways:

1. **OAuth (recommended).** Authorization code with PKCE (S256 only). See below.
2. **A Hosbah user token (`hbu_…`).**
   - `GET /api/integrations/apps` lists the apps the token's user can manage (owner or admin).
   - `POST /api/integrations/apps/{app}/tokens {label}` returns `{token, app}`.
3. **A CircleXO hub token.** These are the same two routes, with these rules:
   - **Audience:** the token must be issued for Hosbah's hub app.
   - **Act chain:** the token must carry one, meaning it was exchanged for an integration.
   - **Identity:** the subject and org are mapped through `circlexo_user_links` and `circlexo_org_links`, as `/api/mcp` does.
   - **Allowed integrations:** if `HOSBAH_INTEGRATION_ACTORS` is set (comma-separated hub subjects), one actor in the chain must be on that list.
   - **Errors:** a token without an act chain, or with an actor that is not allowed, gets 403.

## OAuth

Register the client once, on the control plane:

```sh
go run ./cmd/admin oauth-client add Mahaam https://mahaam.example/integrations/hosbah/callback
# prints client_id (hbc_…) and client_secret (hbs_…) once; only the secret's hash is stored
go run ./cmd/admin oauth-client list
go run ./cmd/admin oauth-client revoke hbc_…
```

Redirect URIs must match exactly. They must be https, or http on localhost.

**`GET /oauth/authorize?response_type=code&client_id&redirect_uri&state&code_challenge&code_challenge_method=S256&scope=app&ui_locales=en|ar`**

- `ui_locales` picks the language.
- The person signs in (CircleXO or local), picks one of the apps they manage, and is sent back with `?code&state`.
- On failure they are sent back with `?error&error_description&state`. The possible errors are `access_denied`, `invalid_request`, `invalid_scope` and `unsupported_response_type`.
- If the client is unknown or the redirect URI is not registered, the page shows the error and does not redirect.

**`POST /oauth/token`** takes form fields:
- `grant_type=authorization_code`, `code`, `redirect_uri`, `code_verifier`
- `client_id` and `client_secret`, either in the form or as HTTP Basic

A successful exchange returns:

```json
{"access_token": "hba_…", "token_type": "Bearer",
 "app": {"id", "name", "title", "status", "server_id", "server_name", "domains", "panel_url"}}
```

How codes behave:
- A code lasts five minutes and can be used once.
- If an exchange fails after the code was found (wrong verifier, wrong redirect URI, or the person can no longer manage the app), the code is burned.
- Errors use the RFC 6749 shape `{error, error_description}`: `invalid_client` (401), `invalid_grant`, `invalid_request` and `unsupported_grant_type`.

The web app proxies `/oauth/token` to the API (`web/next.config.mjs`). In production that path, like `/api`, must reach the API.

## With an app token

All of these use `Authorization: Bearer hba_…`. Errors are `{detail}`, and a 401 means the client should reconnect.

- `GET /api/integrations/app`: the app.
- `POST /api/integrations/app/deployments`: deploy the app.
- `POST /api/integrations/app/panel-sessions {email, name, locale}`: returns `{url, expires_at}`, a one-use link that opens the app's customer panel.
- `DELETE /api/integrations/app/token`: disconnect.

To revoke tokens, go to the app's **Settings → Integration tokens**.
