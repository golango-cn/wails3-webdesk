# CLAUDE.md — WebDesk Development Guide

## Project Overview

WebDesk is a desktop application built with [Wails v3](https://v3.wails.io/) (Go backend + web frontend) that lets users configure websites and open them as standalone app windows via Chrome `--app` mode. Key features: site management, 10 color themes, custom titlebar, window opacity control, and auto-fill login credentials.

## Architecture

```
main.go              — App entry point, window setup, --debug flag, logging init
siteservice.go       — Core service: site CRUD, Chrome launch, CDP injection, window tracking
cdp_client.go        — Chrome DevTools Protocol client (custom WebSocket, no dependencies)
crypto.go            — AES-GCM encryption for stored passwords (machine-key derived)
logging.go           — File-based logging with optional stdout (debug mode)
x11helper_linux.go   — Linux: X11 window management (activate, opacity, WM_CLASS)
x11helper_other.go   — Windows: Win32 opacity, stubs for X11 functions
autofill-extension/  — Chrome Manifest V3 extension (backup auto-fill mechanism)
frontend/dist/       — Vanilla JS frontend (single page app)
```

## Build Commands

### Linux
```bash
wails3 build              # Production build
go build -tags gtk3 .     # Direct build
```

### Windows
```bash
# Production (no console window, `-H windowsgui`)
GOOS=windows GOARCH=amd64 go build -tags gtk3 -ldflags="-H windowsgui" -o build/bin/webdesk.exe .

# Debug (with console window for live logs)
GOOS=windows GOARCH=amd64 go build -tags gtk3 -o build/bin/webdesk-debug.exe .
```

### Using Taskfile
```bash
task build                 # Linux production
task build-windows         # Windows production (no console)
task build-windows-debug   # Windows debug (with console)
```

## Debug Mode

### Enabling Debug

Run with `--debug` flag:
```bash
./webdesk --debug          # Linux
webdesk-debug.exe          # Windows (built without -H windowsgui)
webdesk.exe --debug        # Windows production also supports it (logs to file + console)
```

When debug mode is ON:
- Logs go to **both** file and stdout (visible in terminal/console)
- Windows debug build shows a console window (cmd) with live logs

When debug mode is OFF (default):
- Logs go to **file only**
- Windows production build has no console window

### Log File Location

Logs are written to date-based files:
- **Linux**: `~/.cache/webdesk/logs/webdesk-YYYY-MM-DD.log`
- **Windows**: `%LOCALAPPDATA%\webdesk\logs\webdesk-YYYY-MM-DD.log`

## Auto-Fill System

### How It Works

1. User configures username/password for a site in the WebDesk UI
2. Password is encrypted with AES-GCM using a machine-derived key (hostname + MAC)
3. When opening a site in Chrome mode, WebDesk:
   - Launches Chrome with `--app=URL --user-data-dir=... --remote-debugging-port=9222`
   - Connects to Chrome via CDP (Chrome DevTools Protocol)
   - Registers fill script via `Page.addScriptToEvaluateOnNewDocument` (fires on every page load)
   - Also finds the specific target page and injects via `Runtime.evaluate` (backup)

### Fill Script Strategy (in cdp_client.go → generateFillScript)

**Password field detection**: `input[type="password"]` or `autocomplete="current-password|new-password"`

**Username field detection** (priority order):
1. DOM order: text input immediately before password field in DOM
2. Autocomplete: `autocomplete="username|email"`
3. Keywords: name/id/placeholder matching `user|name|email|account|login`
4. Fallback: first visible text input in form scope

**Value setting (setVal)**: Clears value → native setter → InputEvent (Vue 3 compatible) → change event → focusout/blur

**Login button (clickLoginButton)**: Known IDs → submit buttons → text matching (handles spaces like "登  录") → nearby buttons. Uses `forceClick` to remove `disabled` attribute.

**Session control**: `sessionStorage['webdesk_autofill_done_<hostname>']` prevents re-filling after successful login. Cleared when window closes.

### CDP Client

Custom WebSocket implementation in `cdp_client.go` — no external dependencies. Handles:
- WebSocket handshake (upgrade)
- Frame masking/unmasking
- Ping/pong
- Command/response correlation by ID

## Chrome Launch Logic

### Port Strategy

Fixed debug port **9222**. Single Chrome instance per user-data-dir.

### OpenSite Flow

```
1. Check openWindows → activate if already open
2. Check pendingWindows → skip if pending
3. Kill zombie Chrome (running but no visible pages)
4. If Chrome not running: remove lock files, launch fresh with --app
5. If Chrome running: exec.Command(chrome --app=URL) forwards to existing instance
6. adoptChromeWindow → track window (Linux only, stubs on Windows)
7. injectAutoFillViaCDP → register fill script
```

### Zombie Chrome Detection

When user closes the Chrome app window but Chrome process stays running (no visible pages), `chromeHasVisiblePages()` detects this and kills the process before re-launching.

## Key Files to Know

| File | What to modify for... |
|------|----------------------|
| `cdp_client.go` | Fill script logic, username/password detection, login button finding |
| `siteservice.go` | Chrome launch, CDP injection, window management, site CRUD |
| `crypto.go` | Password encryption/decryption, URL matching |
| `logging.go` | Logging configuration, log format |
| `frontend/dist/src/main.js` | UI: site list, modals, themes, openSite handler |
| `frontend/dist/index.html` | HTML structure, modal forms |
| `frontend/dist/style.css` | All styles, theme variables |
| `autofill-extension/` | Chrome extension content script (backup mechanism) |

## Common Development Tasks

### Adding a new login button ID
In `cdp_client.go`, find `knownIds` in `clickLoginButton()`:
```javascript
var knownIds = ['logButton', 'loginBtn', ...];
```

### Adding a new username detection pattern
In `cdp_client.go`, find the keyword regex in `findUsernameField()`:
```javascript
if (/user|name|email|account|login|手机|账号|账户/.test(n))
```

### Changing the Chrome debug port
Search for `9222` in `siteservice.go` and `cdp_client.go`.

### Adjusting fill timing
In `cdp_client.go`, `generateFillScript()`:
- `setTimeout(fill, 800)` — first fill attempt delay
- `setInterval(fill, 1000)` — polling interval
- `count >= 8` — max poll attempts
- `setTimeout(clickLoginButton, 1000)` — auto-login click delay

In `siteservice.go`, `injectAutoFillViaCDP()`:
- `time.Sleep(500 * time.Millisecond)` — wait before Runtime.evaluate

## Wails v3 Notes

- Frontend bindings are auto-generated in `frontend/bindings/` and `frontend/dist/bindings/`
- After adding new Go methods to SiteService, regenerate bindings with `wails3 generate`
- The `wails.json` config uses `GoTags: "gtk3"` for Linux builds
- Build config in `build/config.yml` controls `wails3 dev` behavior
