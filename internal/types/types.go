package types

// Session represents an authenticated Bluesky session.
type Session struct {
	Did        string `json:"did"`
	Handle     string `json:"handle"`
	AccessJwt  string `json:"accessJwt"`
	RefreshJwt string `json:"refreshJwt"`
	PDSHost    string `json:"pdsHost"`
}

// CleanupRequest represents the user's cleanup configuration.
type CleanupRequest struct {
	Identifier         string   `json:"identifier"`
	AppPassword        string   `json:"app_password"`
	CleanupTypes       []string `json:"cleanup_types"`
	DeleteUntilDaysAgo int      `json:"delete_until_days_ago"`
	ActuallyDelete     bool     `json:"actually_delete_stuff"`
}

// RecordType represents a Bluesky record type for cleanup.
type RecordType string

const (
	RecordTypePost      RecordType = "post"
	RecordTypePostMedia RecordType = "post_with_media"
	RecordTypeLike      RecordType = "like"
	RecordTypeRepost    RecordType = "repost"
	RecordTypeFollow    RecordType = "follow"
	RecordTypeListItem  RecordType = "listitem"
)

// AllRecordTypes returns all supported cleanup types.
func AllRecordTypes() []RecordType {
	return []RecordType{
		RecordTypePost,
		RecordTypePostMedia,
		RecordTypeLike,
		RecordTypeRepost,
		RecordTypeFollow,
		RecordTypeListItem,
	}
}

// RecordToDelete represents a record scheduled for deletion.
type RecordToDelete struct {
	Collection string `json:"collection"`
	Rkey       string `json:"rkey"`
	RecordType string `json:"record_type"`
	CreatedAt  string `json:"created_at"`
}

// Progress represents the current cleanup progress.
type Progress struct {
	State          string `json:"state"` // "idle", "scanning", "deleting", "completed", "cancelled", "error"
	RecordsFound   int64  `json:"records_found"`
	RecordsDeleted int64  `json:"records_deleted"`
	RecordsSkipped int64  `json:"records_skipped"`
	EstRemaining   int64  `json:"est_remaining"`
	CurrentAction  string `json:"current_action"`
	ErrorMessage   string `json:"error_message,omitempty"`
	IsDryRun       bool   `json:"is_dry_run"`
}

// RateLimitConfig configures the rate limiter.
type RateLimitConfig struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	MaxPerHour        int     `json:"max_per_hour"`
	MaxPerDay         int     `json:"max_per_day"`
	BatchSize         int     `json:"batch_size"`
}

// DefaultRateLimitConfig returns the default rate limit configuration
// matching the original tool's limits.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		RequestsPerSecond: 4,
		MaxPerHour:        4000,
		MaxPerDay:         30000,
		BatchSize:         10,
	}
}
