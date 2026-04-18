package scanner

import (
	"context"
	"fmt"
	"strings"
	"time"

	atproto "github.com/bluesky-social/indigo/api/atproto"
	bsky "github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/tinybluerobots/skeetdelete/internal/progress"
	"github.com/tinybluerobots/skeetdelete/internal/types"
)

var collectionMap = map[string]string{
	string(types.RecordTypePost):      "app.bsky.feed.post",
	string(types.RecordTypePostMedia): "app.bsky.feed.post",
	string(types.RecordTypeLike):      "app.bsky.feed.like",
	string(types.RecordTypeRepost):    "app.bsky.feed.repost",
	string(types.RecordTypeFollow):    "app.bsky.graph.follow",
	string(types.RecordTypeListItem):  "app.bsky.graph.listitem",
}

type AuthClient interface {
	WithClient(fn func(*xrpc.Client) error) error
	GetSession() *types.Session
}

type Scanner struct {
	auth AuthClient
	prog *progress.Tracker
}

func NewScanner(auth AuthClient, prog *progress.Tracker) *Scanner {
	return &Scanner{
		auth: auth,
		prog: prog,
	}
}

func (s *Scanner) ScanRecords(ctx context.Context, cfg types.CleanupRequest) ([]types.RecordToDelete, error) {
	cutoff := time.Now().AddDate(0, 0, -cfg.DeleteUntilDaysAgo)
	cleanupSet := make(map[string]bool, len(cfg.CleanupTypes))
	for _, ct := range cfg.CleanupTypes {
		cleanupSet[ct] = true
	}

	collections := collectionsFromConfig(cfg)

	s.prog.SetCurrentAction("Scanning repo records")
	s.prog.SetState("scanning")

	var toDelete []types.RecordToDelete

	for _, coll := range collections {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		s.prog.SetCurrentAction(fmt.Sprintf("Scanning %s", coll))

		records, err := s.listCollection(ctx, coll, cfg, cutoff, cleanupSet)
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", coll, err)
		}
		toDelete = append(toDelete, records...)
	}

	s.prog.SetCurrentAction(fmt.Sprintf("Found %d records to delete", len(toDelete)))
	return toDelete, nil
}

func (s *Scanner) listCollection(ctx context.Context, collection string, cfg types.CleanupRequest, cutoff time.Time, cleanupSet map[string]bool) ([]types.RecordToDelete, error) {
	var toDelete []types.RecordToDelete
	var cursor string

	session := s.auth.GetSession()
	if session == nil {
		return nil, fmt.Errorf("not authenticated")
	}
	did := session.Did

	const maxPages = 10000
	seenCursors := make(map[string]bool, maxPages)

	for page := 0; page < maxPages; page++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		var out *atproto.RepoListRecords_Output
		err := s.auth.WithClient(func(c *xrpc.Client) error {
			var listErr error
			out, listErr = atproto.RepoListRecords(ctx, c, collection, cursor, 100, did, true)
			return listErr
		})
		if err != nil {
			return nil, fmt.Errorf("listing records: %w", err)
		}

		for _, rec := range out.Records {
			rkey := rkeyFromURI(rec.Uri)
			if rkey == "" {
				s.prog.IncrementSkipped()
				continue
			}

			result := filterRecord(rec, collection, rkey, cutoff, cleanupSet)
			if result != nil {
				s.prog.IncrementFound()
				toDelete = append(toDelete, *result)
			} else {
				s.prog.IncrementSkipped()
			}
		}

		if out.Cursor == nil || *out.Cursor == "" {
			break
		}
		nextCursor := *out.Cursor
		if seenCursors[nextCursor] {
			break
		}
		seenCursors[nextCursor] = true
		cursor = nextCursor
	}

	return toDelete, nil
}

func rkeyFromURI(uri string) string {
	parts := strings.Split(strings.TrimRight(uri, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	rkey := parts[len(parts)-1]
	if rkey == "" {
		return ""
	}
	return rkey
}

func filterRecord(rec *atproto.RepoListRecords_Record, collection, rkey string, cutoff time.Time, cleanupSet map[string]bool) *types.RecordToDelete {
	if rec.Value == nil || rec.Value.Val == nil {
		return nil
	}

	switch v := rec.Value.Val.(type) {
	case *bsky.FeedPost:
		return filterPost(v, collection, rkey, cutoff, cleanupSet)
	case *bsky.FeedLike:
		if !cleanupSet[string(types.RecordTypeLike)] {
			return nil
		}
		createdAt, err := time.Parse(time.RFC3339Nano, v.CreatedAt)
		if err != nil || createdAt.After(cutoff) {
			return nil
		}
		return &types.RecordToDelete{
			Collection: collection,
			Rkey:       rkey,
			RecordType: string(types.RecordTypeLike),
			CreatedAt:  createdAt.Format(time.RFC3339),
		}
	case *bsky.FeedRepost:
		if !cleanupSet[string(types.RecordTypeRepost)] {
			return nil
		}
		createdAt, err := time.Parse(time.RFC3339Nano, v.CreatedAt)
		if err != nil || createdAt.After(cutoff) {
			return nil
		}
		return &types.RecordToDelete{
			Collection: collection,
			Rkey:       rkey,
			RecordType: string(types.RecordTypeRepost),
			CreatedAt:  createdAt.Format(time.RFC3339),
		}
	case *bsky.GraphFollow:
		if !cleanupSet[string(types.RecordTypeFollow)] {
			return nil
		}
		createdAt, err := time.Parse(time.RFC3339Nano, v.CreatedAt)
		if err != nil || createdAt.After(cutoff) {
			return nil
		}
		return &types.RecordToDelete{
			Collection: collection,
			Rkey:       rkey,
			RecordType: string(types.RecordTypeFollow),
			CreatedAt:  createdAt.Format(time.RFC3339),
		}
	case *bsky.GraphListitem:
		if !cleanupSet[string(types.RecordTypeListItem)] {
			return nil
		}
		createdAt, err := time.Parse(time.RFC3339Nano, v.CreatedAt)
		if err != nil || createdAt.After(cutoff) {
			return nil
		}
		return &types.RecordToDelete{
			Collection: collection,
			Rkey:       rkey,
			RecordType: string(types.RecordTypeListItem),
			CreatedAt:  createdAt.Format(time.RFC3339),
		}
	case *bsky.FeedPostgate, *bsky.FeedThreadgate:
		return nil
	default:
		return nil
	}
}

func filterPost(post *bsky.FeedPost, collection, rkey string, cutoff time.Time, cleanupSet map[string]bool) *types.RecordToDelete {
	createdAt, err := time.Parse(time.RFC3339Nano, post.CreatedAt)
	if err != nil || createdAt.After(cutoff) {
		return nil
	}

	hasMedia := false
	if post.Embed != nil {
		embedType := post.Embed.EmbedImages != nil || post.Embed.EmbedVideo != nil || post.Embed.EmbedRecordWithMedia != nil
		hasMedia = embedType
	}

	if hasMedia {
		if !cleanupSet[string(types.RecordTypePostMedia)] {
			return nil
		}
		return &types.RecordToDelete{
			Collection: collection,
			Rkey:       rkey,
			RecordType: string(types.RecordTypePostMedia),
			CreatedAt:  createdAt.Format(time.RFC3339),
			Text:       post.Text,
		}
	}

	if !cleanupSet[string(types.RecordTypePost)] {
		return nil
	}
	return &types.RecordToDelete{
		Collection: collection,
		Rkey:       rkey,
		RecordType: string(types.RecordTypePost),
		CreatedAt:  createdAt.Format(time.RFC3339),
		Text:       post.Text,
	}
}

func collectionsFromConfig(cfg types.CleanupRequest) []string {
	seen := make(map[string]bool)
	var collections []string

	for _, ct := range cfg.CleanupTypes {
		if coll, ok := collectionMap[ct]; ok && !seen[coll] {
			seen[coll] = true
			collections = append(collections, coll)
		}
	}

	if len(collections) == 0 {
		collections = []string{
			"app.bsky.feed.post",
			"app.bsky.feed.like",
			"app.bsky.feed.repost",
			"app.bsky.graph.follow",
			"app.bsky.graph.listitem",
		}
	}

	return collections
}
