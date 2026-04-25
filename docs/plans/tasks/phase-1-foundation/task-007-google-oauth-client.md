---
id: TASK-007
title: "Google OAuth client (golang.org/x/oauth2, ID token validation)"
spec: SPEC-002
arch: ARCH-002
status: done
priority: p0
prerequisites: [TASK-005]
skills: [green-bar]
created: 2026-04-20
author: architect
---

# TASK-007: Google OAuth client (golang.org/x/oauth2, ID token validation)

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Implement the Google OAuth client in `internal/auth/google.go` that wraps `golang.org/x/oauth2` for the authorization code flow and provides ID token (JWT) validation using Google's JWKS public keys. This component handles all communication with Google's OAuth endpoints.

## Context

TASK-005 adds `golang.org/x/oauth2` to go.mod and creates the auth configuration. This task implements the Google-specific OAuth logic as a standalone component that the auth service (TASK-006) will call during the callback flow. The component must:
- Generate authorization URLs with state parameter
- Exchange authorization codes for tokens
- Extract and validate the ID token (JWT) from the token response
- Cache Google's JWKS public keys for signature verification

### Files to Read Before Starting

- `.claude/rules/go-style.md` -- Go style conventions
- `.claude/rules/testing.md` -- test conventions
- `docs/plans/architecture/phase-1-foundation/arch-admin-auth.md` -- GoogleClient and JWKSCache sections
- `go.mod` -- verify golang.org/x/oauth2 is present (from TASK-005)

## Requirements

1. Create `internal/auth/google.go` with:
   - `GoogleClient` struct holding an `*oauth2.Config` and a `*JWKSCache`
   - `NewGoogleClient(clientID, clientSecret, redirectURL string) *GoogleClient` constructor
   - The oauth2.Config uses:
     - `Scopes: []string{"openid", "email", "profile"}`
     - `Endpoint: google.Endpoint` (from `golang.org/x/oauth2/google`)
   - `AuthorizationURL(state string) string` -- returns the Google auth URL with the given state, using `oauth2.AccessTypeOnline`
   - `ExchangeCode(ctx context.Context, code string) (idToken string, err error)` -- exchanges code for tokens using the oauth2 library, extracts `id_token` from the token's Extra fields
   - `ValidateIDToken(ctx context.Context, rawIDToken string, expectedAudience string) (email, displayName string, err error)` -- parses and validates the JWT

2. Create `internal/auth/jwks.go` with:
   - `JWKSCache` struct with:
     - A `sync.RWMutex` for thread-safe access
     - A map of key ID (kid) to `*rsa.PublicKey`
     - An expiration time for the cache
     - The JWKS URL (default: `https://www.googleapis.com/oauth2/v3/certs`)
     - An `*http.Client` for fetching keys
   - `NewJWKSCache(url string, client *http.Client) *JWKSCache`
   - `GetKey(ctx context.Context, kid string) (*rsa.PublicKey, error)` -- returns the public key for the given kid, fetching fresh keys if the cache is expired or the kid is not found

3. The ID token validation in `ValidateIDToken` must:
   - Split the JWT into header, payload, and signature parts
   - Decode the header (base64url) to extract the `kid` (key ID) and `alg` (must be RS256)
   - Fetch the public key from the JWKS cache using the `kid`
   - Verify the RS256 signature over the header.payload using the public key
   - Decode the payload (base64url) to extract claims
   - Validate the `iss` claim is `accounts.google.com` or `https://accounts.google.com`
   - Validate the `aud` claim matches the expected audience (client ID)
   - Validate the `exp` claim is in the future
   - Extract and return `email` and `name` (display name) from the claims

4. The JWKS cache must:
   - Fetch keys from the configured URL on first access or when expired
   - Parse the `Cache-Control: max-age=N` header from the response to determine cache TTL (default to 1 hour if not present)
   - Parse the JWKS response format: `{"keys": [{"kid": "...", "n": "...", "e": "...", "kty": "RSA", ...}]}`
   - Convert the JWK `n` (modulus) and `e` (exponent) from base64url to `*rsa.PublicKey`
   - Use `sync.RWMutex` for thread-safe reads during concurrent requests

5. Error handling:
   - Return descriptive errors for each validation failure (expired token, invalid signature, wrong audience, etc.)
   - Never expose raw token values in error messages
   - Wrap errors with context using `fmt.Errorf`

## Acceptance Criteria

- [ ] AC-8: When a user clicks "Sign in with Google", AuthorizationURL returns a URL with correct client_id, redirect_uri, scope, and state parameters.
- [ ] AC-9: When Google redirects back with a valid code, ExchangeCode successfully exchanges it for tokens (tested with httptest mock).
- [ ] AC-10: When the OAuth callback receives a mismatched state parameter, the caller (handler) rejects it (this task ensures state is passed through correctly; the handler validates it).
- [ ] ID token validation rejects tokens with invalid signatures.
- [ ] ID token validation rejects tokens with wrong audience.
- [ ] ID token validation rejects expired tokens.
- [ ] JWKS cache fetches and caches keys, reusing cached keys on subsequent calls.

## Skills to Use

- `green-bar` -- run before marking complete

## Test Requirements

1. Test `AuthorizationURL` generates a URL containing the client_id, redirect_uri, scope, state, and response_type=code parameters.
2. Test `ExchangeCode` with a mock HTTP server that mimics Google's token endpoint -- verify it extracts the id_token from the response.
3. Test `ValidateIDToken` with a self-signed test JWT:
   - Generate an RSA key pair for testing
   - Create a valid JWT signed with the test private key
   - Set up a mock JWKS endpoint serving the test public key
   - Verify successful validation returns correct email and name
4. Test `ValidateIDToken` rejects a JWT with an invalid signature (signed with a different key).
5. Test `ValidateIDToken` rejects a JWT with an expired `exp` claim.
6. Test `ValidateIDToken` rejects a JWT with wrong `aud` claim.
7. Test `ValidateIDToken` rejects a JWT with wrong `iss` claim.
8. Test `JWKSCache` caches keys and does not re-fetch within the TTL.
9. Test `JWKSCache` re-fetches when the cache is expired.
10. Use `httptest.NewServer` to mock Google endpoints in tests.
11. Follow `.claude/rules/testing.md` -- table-driven for validation error cases.

## Definition of Done

- [ ] GoogleClient struct with NewGoogleClient, AuthorizationURL, ExchangeCode, ValidateIDToken implemented
- [ ] JWKSCache with GetKey, automatic refresh, thread-safe access implemented
- [ ] JWT parsing and RS256 signature validation implemented using stdlib crypto
- [ ] All tests pass including mock Google endpoint tests
- [ ] green-bar passes (gofmt, vet, build, test)
- [ ] No new dependencies beyond golang.org/x/oauth2 (added in TASK-005)
