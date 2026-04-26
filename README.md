# screens

A screen dashboard style management service.

## Running

```bash
go run .
```

Configuration is environment-driven. In dev mode (`DEV_MODE=true`), a `.env` file is auto-loaded if present. Real environment variables always take precedence.

## Configuration

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `HTTP_HOST` | string | `0.0.0.0` | Address the HTTP server binds to |
| `HTTP_PORT` | int | `8080` | Port the HTTP server listens on |
| `HTTP_READ_TIMEOUT` | duration | `5s` | Maximum duration for reading request |
| `HTTP_WRITE_TIMEOUT` | duration | `10s` | Maximum duration for writing response |
| `HTTP_SHUTDOWN_TIMEOUT` | duration | `30s` | Graceful shutdown timeout |
| `LOG_LEVEL` | string | `info` | Log level (debug, info, warn, error) |
| `DEV_MODE` | bool | auto | Colorized console logging; auto-detected from TTY when unset |
| `DB_PATH` | string | `screens.db` | Path to the SQLite database file |
| `DB_MAX_OPEN_CONNS` | int | `1` | Maximum number of open database connections |
| `DB_MAX_IDLE_CONNS` | int | `1` | Maximum number of idle database connections |
| `DB_CONN_MAX_LIFETIME` | duration | `0` | Maximum connection lifetime (0 = no limit) |
| `ADMIN_EMAIL` | string | *(required)* | Google email of the initial admin |
| `GOOGLE_CLIENT_ID` | string | *(required)* | Google OAuth 2.0 client ID |
| `GOOGLE_CLIENT_SECRET` | string | *(required)* | Google OAuth 2.0 client secret |
| `GOOGLE_REDIRECT_URL` | string | *(required)* | Google OAuth callback URL |
| `SESSION_DURATION` | duration | `168h` | Session lifetime |
| `SESSION_COOKIE_NAME` | string | `screens_session` | Session cookie name |
| `DEVICE_COOKIE_NAME` | string | `screens_device` | Device cookie name |
| `DEVICE_LAST_SEEN_INTERVAL` | duration | `1m` | Throttle interval for updating a device's `last_seen_at` (0 means update on every auth) |
| `DEVICE_LANDING_URL` | string | `/device/` | Path the browser-enrollment flow redirects newly enrolled devices to (must start with `/`) |

## Health Check

```
GET /health
```

Returns the service health status as JSON.
