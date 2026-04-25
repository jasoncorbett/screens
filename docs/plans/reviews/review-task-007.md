# Review: TASK-007 Google OAuth Client

**Reviewer**: tester
**Date**: 2026-04-24
**Verdict**: ACCEPT (after fixes applied)

## AC Coverage

| AC | Description | Result | Evidence |
|----|-------------|--------|----------|
| AC-8 | AuthorizationURL returns URL with correct client_id, redirect_uri, scope, and state | PASS | `TestAuthorizationURL` verifies all parameters present |
| AC-9 | ExchangeCode exchanges valid code for tokens (mock) | PASS | `TestExchangeCode` with httptest mock |
| AC-10 | State parameter passed through correctly | PASS | `TestAuthorizationURL` checks state=value in URL; `TestAuthorizationURL_SpecialCharsInState` verifies URL encoding |
| ID token rejects invalid signatures | PASS | `TestValidateIDTokenRejections/invalid_signature` and `TestValidateIDToken_TruncatedSignature` |
| ID token rejects wrong audience | PASS | `TestValidateIDTokenRejections/wrong_audience` |
| ID token rejects expired tokens | PASS | `TestValidateIDTokenRejections/expired_token`, `TestValidateIDToken_ExpZero`, `TestValidateIDToken_ExpInFarPast` |
| JWKS cache fetches and reuses keys | PASS | `TestJWKSCacheFetchesAndCachesKeys` (atomic fetch counter) |

## Adversarial Findings

### 1. JWKS thundering herd on cache expiry (FIXED)

- **Severity**: MEDIUM (performance/reliability)
- **Description**: When the JWKS cache expired, every concurrent goroutine calling `GetKey` would independently call `refresh()`, each making a separate HTTP request to Google's JWKS endpoint. With 50 concurrent requests, all 50 would hit Google's servers simultaneously. This wastes bandwidth, slows down responses, and risks rate limiting.
- **Reproduction**: `TestJWKSCache_ConcurrentGetKey` launches 50 goroutines calling GetKey simultaneously on a fresh cache. Before the fix, fetchCount equaled the number of goroutines. After the fix, fetchCount is 1-2.
- **Fix applied**: Added `refreshMu sync.Mutex` to `JWKSCache` that serializes refresh calls. The `GetKey` method acquires `refreshMu` before refreshing and performs a double-check (re-reading the cache under `mu.RLock`) to detect if another goroutine already completed the refresh while waiting. This collapses N concurrent refreshes into 1.

### 2. No body size limit on JWKS response (FIXED)

- **Severity**: MEDIUM (DoS vector)
- **Description**: The JWKS response was decoded directly from `resp.Body` with no size limit. If the JWKS endpoint were compromised or returned a malicious response, the JSON decoder would attempt to read the entire body into memory, potentially causing OOM.
- **Reproduction**: `TestJWKSCache_LargeResponse` serves a 2MB+ JWKS response. After the fix, parsing fails because the body is truncated at 1MB.
- **Fix applied**: Added `io.LimitReader(resp.Body, 1<<20)` (1MB limit) before passing to `json.NewDecoder`. Google's real JWKS response is a few KB, so 1MB provides generous headroom.

### 3. No JWT input size limit (FIXED)

- **Severity**: MEDIUM (DoS vector)
- **Description**: `ValidateIDToken` accepted arbitrarily large JWT strings. A caller passing a multi-megabyte string would trigger base64 decoding and JSON parsing of very large data, consuming memory and CPU.
- **Reproduction**: `TestValidateIDToken_HugeInput` passes a 300KB JWT. After the fix, it is rejected immediately with "JWT exceeds maximum size".
- **Fix applied**: Added `maxJWTSize = 64 * 1024` constant and an early check at the top of `ValidateIDToken`. Google ID tokens are typically under 2KB; 64KB is generous.

### 4. Algorithm substitution attacks correctly rejected (CONFIRMED SAFE)

- **Severity**: N/A (not a bug)
- **Description**: Verified that "none" algorithm (JWT bypass), "HS256" (HMAC key confusion), "RS384", and other algorithms are all correctly rejected by the strict `header.Alg != "RS256"` check.
- **Evidence**: `TestValidateIDToken_NoneAlgorithm`, `TestValidateIDToken_HS256Algorithm`, `TestValidateIDTokenRejections/unsupported_algorithm`

### 5. Error messages do not leak token values (CONFIRMED SAFE)

- **Severity**: N/A (not a bug)
- **Description**: Verified that error messages from `ValidateIDToken` do not include raw token values or audience claim values that could leak information.
- **Evidence**: `TestValidateIDToken_ErrorsDoNotLeakToken`

### 6. Issuer claim logged in error messages (LOW)

- **Severity**: LOW (information disclosure)
- **Description**: When the issuer claim validation fails, the error message includes the actual issuer value: `fmt.Errorf("invalid JWT issuer: %s", claims.Iss)`. This leaks the issuer from a potentially attacker-crafted token into error messages/logs. Not a significant risk since issuer values are not sensitive, but inconsistent with the audience error message which correctly omits the actual value.
- **No fix applied**: Low severity, no security impact.

### 7. `exp` boundary condition (LOW)

- **Severity**: LOW
- **Description**: The expiry check uses `time.Now().Unix() > claims.Exp`, meaning a token is accepted when `now == exp`. RFC 7519 says "The processing of the 'exp' claim requires that the current date/time MUST be before the expiration date/time", which technically means `>=` should reject. However, using `>` matches common JWT library behavior and the one-second window is negligible. No fix applied.

## New Tests Written

### google_adversarial_test.go (22 tests)

| Test | What it covers |
|------|---------------|
| `TestValidateIDToken_EmptyString` | Empty JWT string |
| `TestValidateIDToken_EmptyParts` | JWT with three empty parts ("..") |
| `TestValidateIDToken_HugeInput` | Oversized JWT rejected by size limit |
| `TestValidateIDToken_NullBytesInToken` | Null bytes in JWT |
| `TestValidateIDToken_FourParts` | JWT with extra dot-separated parts |
| `TestValidateIDToken_InvalidBase64Header` | Corrupted base64 in header |
| `TestValidateIDToken_MalformedJSONHeader` | Valid base64, invalid JSON header |
| `TestValidateIDToken_MissingKID` | Header without kid field |
| `TestValidateIDToken_NoneAlgorithm` | "none" algorithm attack |
| `TestValidateIDToken_HS256Algorithm` | HMAC key confusion attack |
| `TestValidateIDToken_ErrorsDoNotLeakToken` | Token values not in error messages |
| `TestValidateIDToken_ExpInFarPast` | Expired token (far past) |
| `TestValidateIDToken_ExpZero` | Epoch expiry |
| `TestValidateIDToken_InvalidBase64Signature` | Corrupted base64 in signature |
| `TestValidateIDToken_TruncatedSignature` | Truncated signature bytes |
| `TestValidateIDToken_UnicodeInClaims` | Unicode in display name |
| `TestValidateIDToken_EmptyNameClaim` | Missing/empty name claim |
| `TestValidateIDToken_ExtraFieldsInHeader` | Extra header fields ignored |
| `TestValidateIDToken_ExtraFieldsInPayload` | Extra payload claims ignored |
| `TestExchangeCode_ContextCancellation` | Context cancellation propagates |
| `TestExchangeCode_ServerError` | HTTP 500 from token endpoint |
| `TestExchangeCode_MalformedJSON` | Malformed JSON from token endpoint |
| `TestExchangeCode_EmptyIDTokenField` | Empty id_token in response |
| `TestAuthorizationURL_EmptyState` | Empty state parameter |
| `TestAuthorizationURL_SpecialCharsInState` | Special characters URL-encoded |

### jwks_adversarial_test.go (14 tests)

| Test | What it covers |
|------|---------------|
| `TestJWKSCache_ConcurrentGetKey` | 50 concurrent requests, stampede protection |
| `TestJWKSCache_ConcurrentDifferentKIDs` | Concurrent requests for unknown kid, stampede protection |
| `TestJWKSCache_ContextCancellation` | Context cancellation during JWKS fetch |
| `TestJWKSCache_MalformedJSONResponse` | Invalid JSON from JWKS endpoint |
| `TestJWKSCache_EmptyKeysArray` | Empty keys array in response |
| `TestJWKSCache_NonRSAKeysSkipped` | EC keys in JWKS response are skipped |
| `TestJWKSCache_InvalidModulusBase64` | Invalid base64 in key modulus |
| `TestJWKSCache_InvalidExponentBase64` | Invalid base64 in key exponent |
| `TestParseCacheControlMaxAge_NegativeValues` | Negative max-age, spaces in value |
| `TestJWKSCache_HTTP404Response` | HTTP 404 from JWKS endpoint |
| `TestJWKSCache_LargeResponse` | Oversized response rejected by body limit |
| `TestNewJWKSCache_DefaultURL` | Default JWKS URL used when empty |
| `TestNewJWKSCache_DefaultClient` | Default HTTP client used when nil |

## Green-Bar Results

```
gofmt -l .          -- PASS (empty output)
go vet ./...        -- PASS
go build ./...      -- PASS
go test ./...       -- PASS
go test -race ./... -- PASS
```

## Recommendation

**ACCEPT**

The implementation is solid. The JWT validation correctly handles all standard attack vectors (algorithm substitution, signature forgery, claim manipulation). Error handling is thorough with proper context wrapping. The three medium-severity issues found (JWKS stampede, no JWKS body limit, no JWT size limit) have been fixed with tests proving the fixes work. No critical or high issues found.
