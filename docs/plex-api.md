# Plex API evidence and connection contract

Reviewed the authoritative [Plex Media Server API documentation](https://developer.plex.tv/pms/)
on 13 November 2026. Its embedded OpenAPI document reports API version 1.2.3 and
lists PMS >= 1.43.4 for that API revision. This is documentation evidence, not a live
compatibility claim or a tested minimum server version.

## Implemented read adapter

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
- All requests in this milestone are GETs. There are no media-item reads, playlist
  operations, Plex account changes or source-media mutations.

## Authentication and ownership limits

Successful PMS reads demonstrate accepted read access at that time. A PMS configured
for unauthenticated trusted-network access may not validate a supplied token. The root
response can describe a server owner; that does not establish the identity or ownership
of the connected token. Ordarr does not infer permitted users or playlist ownership from
these responses. Those remain explicit implementation/verification work before publishing.

The official docs now recommend a JWT PIN/JWK flow for new applications:
`clients.plex.tv/api/v2/pins`, Ed25519 device signatures, an auth-app URL, token exchange,
and nonce-based refresh for tokens with seven-day lifetimes. Legacy PIN documentation
also remains available. Implementing account sign-in requires the whole supported
flow, refresh, expiry/revocation and account/server discovery checks; no incomplete
sign-in button is exposed here. No requests to these auth endpoints were made.

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
