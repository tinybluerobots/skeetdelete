package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"syscall/js"
	"time"

	"github.com/tinybluerobots/skeetdelete/internal/auth"
	"github.com/tinybluerobots/skeetdelete/internal/delete"
	"github.com/tinybluerobots/skeetdelete/internal/progress"
	"github.com/tinybluerobots/skeetdelete/internal/rate"
	"github.com/tinybluerobots/skeetdelete/internal/scanner"
	"github.com/tinybluerobots/skeetdelete/internal/types"
)

var (
	tracker    *progress.Tracker
	authModule *auth.Auth
	cancelMu   sync.Mutex
	cancelFn   context.CancelFunc
	running    bool

	loginResultMu sync.Mutex
	loginResult   string
	loginPending  bool

	recordsMu   sync.Mutex
	cachedRecords []types.RecordToDelete

	skeetDelFn    js.Func
	skeetCnlFn    js.Func
	skeetProgFn   js.Func
	skeetPrevFn   js.Func
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			logToJS("PANIC: " + fmt.Sprintf("%v", r))
		}
	}()

	tracker = progress.NewTracker()
	authModule = auth.NewAuth()

	skeetDelFn = js.FuncOf(handleSkeetDelete)
	skeetCnlFn = js.FuncOf(handleSkeetCancel)
	skeetProgFn = js.FuncOf(handleGetProgress)
	skeetPrevFn = js.FuncOf(handleGetPreview)

	js.Global().Set("skeetDelete", skeetDelFn)
	js.Global().Set("skeetCancel", skeetCnlFn)
	js.Global().Set("skeetGetProgress", skeetProgFn)
	js.Global().Set("skeetGetPreview", skeetPrevFn)

	logToJS("SkeetDelete WASM loaded")

	select {}
}

func handleSkeetDelete(this js.Value, args []js.Value) interface{} {
	if len(args) == 0 || !args[0].Truthy() {
		return jsonString(map[string]interface{}{"error": "no configuration provided"})
	}

	cfg := args[0]
	if cfg.Type() == js.TypeString {
		parsed := js.Global().Get("JSON").Call("parse", cfg)
		if !parsed.Truthy() {
			return jsonString(map[string]interface{}{"error": "invalid JSON"})
		}
		cfg = parsed
	}
	action := cfg.Get("action").String()

	switch action {
	case "login":
		return handleLogin(this, args)
	case "cleanup":
		return handleCleanup(cfg)
	case "signout":
		authModule.SetSession(nil)
		return nil
	default:
		return jsonString(map[string]interface{}{"error": "unknown action: " + action})
	}
}

func handleLogin(this js.Value, args []js.Value) interface{} {
	identifier := ""
	password := ""

	if len(args) > 0 && args[0].Truthy() {
		cfg := args[0]
		if cfg.Type() == js.TypeString {
			cfg = js.Global().Get("JSON").Call("parse", cfg)
		}
		if cfg.Truthy() && cfg.Get("action").String() == "login" {
			identifier = cfg.Get("identifier").String()
			password = cfg.Get("appPassword").String()
		}
	}

	if identifier == "" || password == "" {
		return jsonString(map[string]interface{}{"error": "handle and app password are required"})
	}

	loginResultMu.Lock()
	if loginPending {
		loginResultMu.Unlock()
		return jsonString(map[string]interface{}{"error": "login already in progress"})
	}
	loginPending = true
	loginResult = ""
	loginResultMu.Unlock()

	go func() {
		session, err := authModule.CreateSession(context.Background(), identifier, password)
		loginResultMu.Lock()
		if err != nil {
			loginResult = jsonString(map[string]interface{}{"error": err.Error()})
		} else {
			loginResult = jsonString(map[string]interface{}{
				"did":    session.Did,
				"handle": session.Handle,
			})
		}
		loginPending = false
		loginResultMu.Unlock()
	}()

	return nil
}

func handleCleanup(cfg js.Value) interface{} {
	cancelMu.Lock()
	if running {
		cancelMu.Unlock()
		return jsonString(map[string]interface{}{"error": "cleanup already in progress"})
	}
	cancelMu.Unlock()

	loginResultMu.Lock()
	loginResult = ""
	loginResultMu.Unlock()

	recordsMu.Lock()
	cachedRecords = nil
	recordsMu.Unlock()

	var cleanupTypes []string
	if ct := cfg.Get("cleanupTypes"); ct.Truthy() {
		length := ct.Length()
		cleanupTypes = make([]string, length)
		for i := 0; i < length; i++ {
			cleanupTypes[i] = ct.Index(i).String()
		}
	}

	req := types.CleanupRequest{
		CleanupTypes:       cleanupTypes,
		DeleteUntilDaysAgo: cfg.Get("deleteUntilDaysAgo").Int(),
		ActuallyDelete:     cfg.Get("actuallyDelete").Bool(),
	}

	if req.DeleteUntilDaysAgo <= 0 {
		req.DeleteUntilDaysAgo = 90
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancelMu.Lock()
	cancelFn = cancel
	running = true
	cancelMu.Unlock()

	tracker.Update(types.Progress{
		State:    "scanning",
		IsDryRun: !req.ActuallyDelete,
	})

	go runCleanup(ctx, req)

	return nil
}

func handleSkeetCancel(this js.Value, args []js.Value) interface{} {
	cancelMu.Lock()
	if running && cancelFn != nil {
		cancelFn()
		tracker.SetState("cancelled")
	}
	cancelMu.Unlock()
	return nil
}

func handleGetProgress(this js.Value, args []js.Value) interface{} {
	loginResultMu.Lock()
	lr := loginResult
	lp := loginPending
	loginResultMu.Unlock()

	if lp {
		return jsonString(map[string]interface{}{"state": "login_pending"})
	}
	if lr != "" {
		return lr
	}

	return jsonString(tracker.Get())
}

func handleGetPreview(this js.Value, args []js.Value) interface{} {
	limit := 20
	if len(args) > 0 && args[0].Truthy() {
		limit = args[0].Int()
		if limit <= 0 {
			limit = 20
		}
	}

	recordsMu.Lock()
	recs := make([]types.RecordToDelete, len(cachedRecords))
	copy(recs, cachedRecords)
	recordsMu.Unlock()

	sort.Slice(recs, func(i, j int) bool {
		return recs[i].CreatedAt > recs[j].CreatedAt
	})

	if len(recs) > limit {
		recs = recs[:limit]
	}

	return jsonString(map[string]interface{}{
		"records": recs,
		"total":   len(cachedRecords),
	})
}

func runCleanup(ctx context.Context, req types.CleanupRequest) {
	defer func() {
		cancelMu.Lock()
		running = false
		cancelFn = nil
		cancelMu.Unlock()

		if r := recover(); r != nil {
			tracker.SetError(fmt.Sprintf("panic in cleanup: %v", r))
		}
	}()

	if !authModule.IsAuthenticated() {
		tracker.SetError("not authenticated - please sign in first")
		return
	}

	limiter := rate.NewLimiter(types.DefaultRateLimitConfig())
	delEngine := delete.NewEngine(authModule, limiter, tracker)

	scan := scanner.NewScanner(authModule, tracker)

	records, err := scan.ScanRecords(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			tracker.SetState("cancelled")
		} else {
			tracker.SetError(fmt.Sprintf("scanning records: %v", err))
		}
		return
	}

	recordsMu.Lock()
	cachedRecords = records
	recordsMu.Unlock()

	select {
	case <-ctx.Done():
		tracker.SetState("cancelled")
		return
	default:
	}

	if len(records) == 0 {
		tracker.SetCurrentAction("no records found to delete")
		tracker.SetState("completed")
		return
	}

	prog := tracker.Get()
	prog.EstRemaining = int64(len(records))
	tracker.Update(prog)

	if req.ActuallyDelete {
		tracker.SetState("deleting")
		tracker.SetCurrentAction(fmt.Sprintf("deleting %d records", len(records)))

		refreshTicker := time.NewTicker(5 * time.Minute)
		defer refreshTicker.Stop()

		refreshDone := make(chan struct{})
		go func() {
			for {
				select {
				case <-refreshTicker.C:
					if ctx.Err() != nil {
						return
					}
					_, refreshErr := authModule.RefreshSession(ctx)
					if refreshErr != nil {
						logToJS(fmt.Sprintf("session refresh warning: %v", refreshErr))
					}
				case <-refreshDone:
					return
				}
			}
		}()

		err = delEngine.DeleteRecords(ctx, records, req)
		close(refreshDone)
		if err != nil {
			if ctx.Err() != nil {
				tracker.SetState("cancelled")
			} else {
				tracker.SetError(fmt.Sprintf("deletion error: %v", err))
			}
			return
		}
	} else {
		prog := tracker.Get()
		prog.RecordsDeleted = int64(len(records))
		prog.EstRemaining = 0
		tracker.Update(prog)
	}

	tracker.SetCurrentAction(fmt.Sprintf("done - %d records processed", len(records)))
	tracker.SetState("completed")
}

func logToJS(msg string) {
	console := js.Global().Get("console")
	if console.Truthy() {
		console.Call("log", msg)
	}
}

func jsonString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"json marshal error"}`
	}
	return string(b)
}
