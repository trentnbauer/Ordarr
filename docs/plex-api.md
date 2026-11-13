# Plex API evidence and connection contract

Reviewed the authoritative [Plex Media Server API documentation](https://developer.plex.tv/pms/)
on 13 November 2026. Its embedded OpenAPI document reports API version 1.2.3 and
lists PMS >= 1.43.4 for that API revision. This is documentation evidence, not a live
compatibility claim or a tested minimum server version.

## Implemented PMS read adapter

- API Info / Content Types recommends `Accept: application/json`; the default is XML.
- API Info / Headers and Auth defines `X-Plex-Token`, `X-Plex-Client-Identifier`,
  `X-Plex-Product` and `X-Plex-Version`. Tokens are opaque and can be traditional or JWT.
- `GET /` (Get PMS info) returns `MediaContainer.machineIdentifier`, `friendlyName`
  and `version`. The unauthenticated `/identity` endpoint is not sufficient to validate
  a connection's library read access, so it is not used for this check.
- The current Library / Get library sections operation specifies
  `GET /library/sections/all`. Its response contains `MediaContainer.Directory` with
  section `key`, `title` and `type`. On a definite 404 only, Ordarr falls back to
  `GET /library/sections`, which also appears in the official authentication examples
  and [Plex Support token guidance](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/).
  No fallback is performed after 401/403 or malformed/incomplete data.
- API Info / Pagination defines the request headers `X-Plex-Container-Start` and
  `X-Plex-Container-Size`, the response headers `X-Plex-Container-Start` and
  `X-Plex-Container-Total-Size`, and `offset`, `size`, `totalSize` in the container.
  X-Plex headers may also be query parameters. Ordarr sends pagination in headers
  and equivalent non-secret query fields and reconciles body/header pagination.
- Unpaginated responses must not indicate truncation. Paginated snapshots require
  a consistent total and progress at the expected offset. Duplicate keys, changing
  totals, inconsistent sizes, oversized responses and partial scans fail closed.
  The limits are 1 MiB per response and 1,000 libraries. Unknown optional fields
  are ignored; remote response URLs/keys are never followed.
- All PMS requests in this milestone are GETs. There are no media-item reads, playlist
  operations, Plex account changes or source-media mutations.

## Authentication and ownership limits

Successful PMS reads demonstrate accepted read access at that time. A PMS configured
for unauthenticated trusted-network access may not validate a supplied token. The root
response can describe a server owner; that does not establish the identity or ownership
of the connected token. Ordarr does not infer permitted users or playlist ownership from
these PMS responses. Account sign-in verifies the connected user independently, as
described below; other-user permission and playlist write checks remain pending.

## Implemented account JWT/PIN flow

The same official API Info / Authenticating with Plex section documents the recommended
JWT/PIN/JWK flow implemented on 14 November 2026:

1. Generate an Ed25519 device key and stable identifier for that authorisation. POST
   `https://clients.plex.tv/api/v2/pins` with JSON `strong: true` and public JWK fields
   `kty: OKP`, `crv: Ed25519`, `x`, `kid`, `alg: EdDSA`.
2. Render the documented `https://app.plex.tv/auth#?...` fragment URL with `clientID`,
   PIN `code` and `context[device][product]`. Ordarr uses an explicit user-driven poll
   rather than accepting a callback/forward URL. A linked account does not log anyone
   into Ordarr or replace its administrator session.
3. GET `https://clients.plex.tv/api/v2/pins/<pinID>?deviceJWT=<signed assertion>`.
   The Ed25519 assertion includes `aud: plex.tv`, `iss: client ID`, `iat`, `exp`,
   `scope` and header `kid`/`alg: EdDSA`. Pending null `authToken` is not a login.
   Returned PIN ID/code must match the initiated attempt. Local session binding,
   expiry, throttle and atomic consumption protect the attempt.
4. Verify the token with `GET https://plex.tv/api/v2/user`, as documented in the
   token verification section. The adapter expects the current user fields `id`,
   `username`, optional `friendlyName`, `restricted`, and `anonymous`; anonymous or
   malformed identities fail closed. No identity is taken from unverified JWT claims.
5. Discover resources with `GET https://clients.plex.tv/api/v2/resources?includeHttps=1&includeRelay=1&includeIPv6=1`.
   The official Talking to PMS section specifies separate per-PMS `accessToken`s and
   connection URLs. Ordarr expects resource `clientIdentifier`, `name`, `provides`,
   `accessToken`, `owned` and connection `uri`/`local`/`relay`. Server tokens and the
   full resource cache stay encrypted; only safe names/URLs reach templates.
6. Explicitly select a resource URL. It must exactly match a validated URL from the
   verified resource list. Inspect PMS with its resource token, never the account
   token, and compare its machine ID before adding a linked connection.
7. Refresh via `GET https://clients.plex.tv/api/v2/auth/nonce`, then POST
   `https://clients.plex.tv/api/v2/auth/token` with a device-signed JWT containing the
   nonce. The returned `auth_token` is reverified with user/resources calls. Identity
   changes are rejected. The private key and token/cache update are encrypted and
   revision-checked; failed refreshes leave prior data intact.

The official docs state seven-day account JWT lifetimes. The implementation reads
`exp` only to schedule refresh and rejects expired or implausibly long returned JWTs;
it relies on the authenticated Plex user endpoint to validate identity/token validity.
It refreshes within 15 minutes of expiry when an account-linked operation is requested,
and offers explicit refresh/reauthorisation. It performs no background polling.

The required device assertion in the PIN polling query is short-lived and never logged;
Plex account and PMS access tokens stay in headers. The browser sees a short-lived PIN
code in the documented auth-app fragment, never an access token/private key. PIN state
is encrypted, limited to ten minutes, bound to the originating Ordarr session, and
consumed on success. Account operations are CSRF-protected POSTs, including polling,
cancellation, refresh and disconnect. Local logout removes pending attempts.

The cloud API origins are fixed in production and use verified TLS/inherited proxies.
Account operations have a ten-second overall context; requests are capped at five
seconds and are not automatically retried. Responses are limited to 1 MiB and 50 servers
with at most 20 addresses each. Redirects and malformed/partial responses fail closed.
No live account API authorisation was performed; tests use an explicitly injected
transport to disposable HTTP fixtures. PIN response fields, current user/resource
shapes, token refresh/revocation, and service/version behavior still need live verification.

## Validation and operational limits

Fixture-backed tests use the real adapter over HTTP and cover documented and legacy
library routes, body/header pagination, malformed/truncated responses, duplicate IDs,
401/403/498, rate limiting, redirects, TLS failure, timeouts and token reflection.
Retries are restricted to one retry for 502/503/504 without `Retry-After`, delayed by
100 ms; 401/403/429 and explicit Retry-After responses are not retried. An application
connection check has an eight-second context; each request is capped at five seconds.
No token, raw transport error, response body or request URL enters application logs.

A disposable live Plex server is still needed to validate minimum-version behavior,
real library access restrictions, zero-library users, legacy/current pagination,
expired tokens and account identity before claiming real-service compatibility.
Media matching ambiguity and availability tests belong to the pending matching milestone.
