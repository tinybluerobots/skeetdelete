# PROJECT KNOWLEDGE BASE

**Generated:** 2026-04-18
**Branch:** main

## OVERVIEW
Browser-based Bluesky account cleanup tool. Go WebAssembly frontend + AT Protocol API. No traditional server — compiles to `main.wasm`, runs entirely client-side.

## STRUCTURE
```
skeetdelete/
├── cmd/
│   ├── wasm/        # WASM module (core business logic)
│   ├── serve/       # Static file server for production
│   ├── devserver/   # Dev server + API proxy → :8081
│   └── mockserver/  # Mock ATProto API for local testing
├── internal/
│   ├── auth/        # Bluesky session + PDS resolution
│   ├── scanner/     # Repo record scanning + filtering
│   ├── delete/      # Batch deletion engine (applyWrites)
│   ├── rate/        # Token bucket + hourly/daily limits
│   ├── progress/    # JS-callback progress tracker
│   └── types/       # Shared types (Session, CleanupRequest, Progress, etc.)
├── static/          # index.html + compiled WASM artifacts
├── mise.toml        # Build tasks (replaces Makefile)
└── e2e_test.js      # Playwright E2E test
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Add record type for cleanup | `internal/types/types.go` | Add `RecordType` const + `collectionMap` in scanner |
| Change deletion logic | `internal/delete/engine.go` | Uses `atproto.RepoApplyWrites` batched |
| Add API endpoint mock | `cmd/mockserver/main.go` | Add handler + register on mux |
| Change rate limits | `internal/types/types.go:75` | `DefaultRateLimitConfig()` |
| Frontend UI changes | `static/index.html` | Single file (HTML+CSS+JS) |
| JS↔Go bridge | `cmd/wasm/main.go` | `skeetDelete`, `skeetCancel`, `skeetGetProgress` |
| Auth/session flow | `internal/auth/auth.go` | Creates session, resolves PDS host, refresh tokens |

## CODE MAP

| Symbol | Type | Location | Role |
|--------|------|----------|------|
| `handleSkeetDelete` | func | cmd/wasm/main.go | JS entry: routes login/cleanup/signout |
| `handleCleanup` | func | cmd/wasm/main.go | Spawns goroutine for scan+delete |
| `Engine.DeleteRecords` | method | internal/delete/engine.go | Batched delete with retry+backoff |
| `Scanner.ScanRecords` | method | internal/scanner/scanner.go | Paginated repo scan with date filter |
| `Auth.CreateSession` | method | internal/auth/auth.go | Login + PDS host resolution |
| `Auth.RefreshSession` | method | internal/auth/auth.go | Token refresh (swap refreshJwt) |
| `Tracker` | struct | internal/progress/tracker.go | Mutex-protected state → JS callback |
| `Limiter` | struct | internal/rate/limiter.go | Token bucket + hourly/daily windows |

## CONVENTIONS
- **Build tool**: `mise` (not Make). All tasks in `mise.toml`.
- **Import order**: stdlib → external (`github.com/bluesky-social/indigo`) → internal (`skeetdelete/internal/...`)
- **DI pattern**: Interfaces defined in consumer package (`AuthProvider` in delete, `AuthClient` in scanner)
- **Error wrapping**: `fmt.Errorf("context: %w", err)` throughout
- **JSON tags**: camelCase (`accessJwt`, `pdsHost`, `records_found`)
- **Concurrency**: All shared state mutex-protected with getter/setter methods
- **Context**: Propagated to all I/O; cancellation checked in loops

## ANTI-PATTERNS (THIS PROJECT)
- **DO NOT** add a traditional Go HTTP server — business logic compiles to WASM, servers are thin file routers
- **DO NOT** test `cmd/wasm/...` with `go test` — WASM uses `syscall/js`, not testable with standard Go test runner (excluded in `mise run test`)
- **DO NOT** forget to update `collectionMap` in scanner when adding new `RecordType`
- **DO NOT** use `panic()` in internal packages — use error returns; only `cmd/wasm/main.go` has recover() for JS bridge safety

## UNIQUE STYLES
- Single HTML file for entire frontend (no framework, no build step for JS)
- Go→JS bridge via `syscall/js`: `js.Global().Set("skeetDelete", fn)`
- Progress tracking: Go `Tracker` calls `js.Func` callback on every state change
- PDS resolution: PLC directory for `did:plc:`, `did.json` for `did:web:`
- Retry strategy: 3 attempts with throttled-wait + auto session refresh on 401

## COMMANDS
```bash
mise run build          # Production WASM (optimized, stripped)
mise run build-debug    # Debug WASM (with symbols)
mise run compress       # gzip + brotli variants (CI uses this)
mise run serve          # Build + run dev server
mise run test           # Go tests (excludes WASM packages)
mise run lint           # golangci-lint
mise run format         # go fmt
mise run all            # format + lint + test
mise run clean          # Remove WASM artifacts
mise run tidy           # Update + tidy dependencies
```

### E2E Testing (manual)
```bash
# Terminal 1: mock API
go run ./cmd/mockserver
# Terminal 2: dev server
go run ./cmd/devserver
# Terminal 3: run test
node e2e_test.js
```

### Deployment
Push to `main` → GitHub Actions builds WASM + compress → deploys to GitHub Pages.

## NOTES
- Default ports: 8080 (dev/prod server), 8081 (mock API)
- Dry run is ON by default (`actuallyDelete: false`)
- Default rate limits: 4 req/s, 4000/hr, 30000/day, batch size 10
- Session auto-refreshes every 5 minutes during active deletion
- `.wasm`, `.wasm.gz`, `.wasm.br` all gitignored — built in CI
- Pre-compiled binaries at root (`serve`, `devserver`, `mockserver`, `wasm`) are dev artifacts, not tracked