package delete

import (
	"context"
	"errors"
	"fmt"
	"time"

	atproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/tinybluerobots/skeetdelete/internal/progress"
	"github.com/tinybluerobots/skeetdelete/internal/rate"
	"github.com/tinybluerobots/skeetdelete/internal/types"
)

type AuthProvider interface {
	GetSession() *types.Session
	RefreshSession(ctx context.Context) (*types.Session, error)
	WithClient(fn func(*xrpc.Client) error) error
}

type Engine struct {
	auth     AuthProvider
	limiter  *rate.Limiter
	progress *progress.Tracker
}

func NewEngine(auth AuthProvider, limiter *rate.Limiter, prog *progress.Tracker) *Engine {
	return &Engine{
		auth:     auth,
		limiter:  limiter,
		progress: prog,
	}
}

func (e *Engine) DeleteRecords(ctx context.Context, records []types.RecordToDelete, cfg types.CleanupRequest) error {
	if len(records) == 0 {
		return nil
	}

	session := e.auth.GetSession()
	batchSize := types.DefaultRateLimitConfig().BatchSize

	e.progress.SetState("deleting")
	e.progress.SetCurrentAction(fmt.Sprintf("Deleting %d records in batches of %d", len(records), batchSize))

	for i := 0; i < len(records); i += batchSize {
		if ctx.Err() != nil {
			e.progress.SetState("cancelled")
			return ctx.Err()
		}

		if err := e.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limiter wait: %w", err)
		}

		if !e.limiter.CanMakeRequest() {
			e.progress.SetError("rate limit reached (hourly or daily)")
			return fmt.Errorf("rate limit reached")
		}

		end := i + batchSize
		if end > len(records) {
			end = len(records)
		}
		batch := records[i:end]

		if cfg.ActuallyDelete {
			if err := e.sendBatch(ctx, session.Did, batch); err != nil {
				return err
			}
			e.limiter.RecordRequest()
		}

		for range batch {
			e.progress.IncrementDeleted()
		}

		e.progress.SetCurrentAction(fmt.Sprintf("Processing batch %d/%d", (i/batchSize)+1, (len(records)+batchSize-1)/batchSize))
	}

	e.progress.SetState("completed")
	e.progress.SetCurrentAction("Deletion complete")
	return nil
}

func (e *Engine) sendBatch(ctx context.Context, repoDid string, batch []types.RecordToDelete) error {
	writes := make([]*atproto.RepoApplyWrites_Input_Writes_Elem, 0, len(batch))
	for _, rec := range batch {
		writes = append(writes, &atproto.RepoApplyWrites_Input_Writes_Elem{
			RepoApplyWrites_Delete: &atproto.RepoApplyWrites_Delete{
				LexiconTypeID: "com.atproto.repo.applyWrites#delete",
				Collection:    rec.Collection,
				Rkey:          rec.Rkey,
			},
		})
	}

	input := &atproto.RepoApplyWrites_Input{
		Repo:   repoDid,
		Writes: writes,
	}

	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		var apiErr error
		err := e.auth.WithClient(func(c *xrpc.Client) error {
			_, apiErr = atproto.RepoApplyWrites(ctx, c, input)
			return nil
		})
		if err != nil {
			return fmt.Errorf("sending batch: %w", err)
		}
		if apiErr == nil {
			return nil
		}

		var xrpcErr *xrpc.Error
		if !errors.As(apiErr, &xrpcErr) {
			return fmt.Errorf("sending batch: %w", apiErr)
		}

		if xrpcErr.IsThrottled() {
			if xrpcErr.Ratelimit != nil {
				waitDur := time.Until(xrpcErr.Ratelimit.Reset)
				if waitDur > 0 && waitDur < 5*time.Minute {
					select {
					case <-time.After(waitDur):
						continue
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
			select {
			case <-time.After(2 * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if xrpcErr.StatusCode == 401 {
			if _, refreshErr := e.auth.RefreshSession(ctx); refreshErr != nil {
				return fmt.Errorf("auth expired and refresh failed: %w", refreshErr)
			}
			continue
		}

		return fmt.Errorf("sending batch: %w", apiErr)
	}

	return fmt.Errorf("sending batch: max retries exceeded")
}
