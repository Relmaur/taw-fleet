package companion

import "encoding/json"

// The publish routes' shapes (companion 0.5, ADR-0006; the work is taw/core
// 1.98's Content\Publish). Values stay raw JSON: a field's value can be text,
// a number, a list of rows; the review renders them.

// PublishPlan is POST /content/publish/plan's answer.
type PublishPlan struct {
	ID        string `json:"publish_id"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	Plan      struct {
		RegistryDrift []string     `json:"registry_drift"`
		Records       []PlanRecord `json:"records"`
		Warnings      []string     `json:"warnings"`
	} `json:"plan"`
	Media struct {
		Needed []MediaFile `json:"needed"`
	} `json:"media"`
	Limits map[string]int64 `json:"limits"`
}

// PlanRecord is one record the publish would change: a post, term, option,
// menu, theme mod, or a file to upload (kind "media").
type PlanRecord struct {
	Key      string            `json:"record_key"`
	Hash     string            `json:"hash"`
	Kind     string            `json:"kind"`
	Type     string            `json:"type,omitempty"`
	Slug     string            `json:"slug,omitempty"`
	Path     string            `json:"path,omitempty"`
	Name     string            `json:"key,omitempty"` // an option's or menu's own name, a file's path
	Title    string            `json:"title,omitempty"`
	EditURL  string            `json:"edit_url,omitempty"`
	Op       string            `json:"op"` // create, update, would-delete, missing
	Conflict *bool             `json:"conflict,omitempty"`
	Changes  map[string]Change `json:"changes"`
	// A file to upload (kind "media").
	File   string `json:"file,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
	Mime   string `json:"mime,omitempty"`
}

// Change is one value a record changes: production's (Old) and the incoming
// one (New), with what the field is and, against the base, who changed it.
type Change struct {
	Status       string          `json:"status"` // new, changed, missing
	Old          json.RawMessage `json:"old,omitempty"`
	New          json.RawMessage `json:"new,omitempty"`
	Base         json.RawMessage `json:"base,omitempty"`
	Label        string          `json:"label,omitempty"`
	Type         string          `json:"type,omitempty"`
	MetaboxID    string          `json:"metabox_id,omitempty"`
	MetaboxTitle string          `json:"metabox_title,omitempty"`
	BlockID      string          `json:"block_id,omitempty"`
	ParentGroup  string          `json:"parent_group,omitempty"`
	OptionPage   string          `json:"option_page_title,omitempty"`
	// Conflict: production changed it since the base (nil: no telling).
	Conflict *bool `json:"conflict,omitempty"`
	// LocalChanged: the incoming value differs from the base.
	LocalChanged *bool       `json:"local_changed,omitempty"`
	Media        *MediaFacts `json:"media,omitempty"`
}

// MediaFacts are the files an image, files or featured-image change is about.
type MediaFacts struct {
	Old []MediaRef `json:"old"`
	New []MediaRef `json:"new"`
}

// MediaRef is one file: production's (URL), or one the publish brings
// (Incoming: File is its path under Local's uploads).
type MediaRef struct {
	ID       int    `json:"id,omitempty"`
	File     string `json:"file,omitempty"`
	URL      string `json:"url,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Incoming bool   `json:"incoming,omitempty"`
}

// MediaFile is a file the plan needs uploaded, by its bytes.
type MediaFile struct {
	File   string `json:"file"` // its original's path under uploads (yyyy/mm/name)
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Mime   string `json:"mime"`
}

// Approval names a record to publish with the hash it had in the plan.
type Approval struct {
	Key  string `json:"key"`
	Hash string `json:"hash"`
}

// ApplyRequest is POST /content/publish/{id}/apply's body.
type ApplyRequest struct {
	Token    string              `json:"token"`
	Approved []Approval          `json:"approved"`
	Keep     map[string][]string `json:"keep"` // record key → values kept as production has them
	Take     map[string][]string `json:"take"` // record key → conflicting values the incoming side wins
}

// ApplyResult is the apply's answer: taw/core's import report.
type ApplyResult struct {
	Report struct {
		Created         []json.RawMessage `json:"created"`
		Updated         []json.RawMessage `json:"updated"`
		Skipped         []json.RawMessage `json:"skipped"`
		Deleted         []json.RawMessage `json:"deleted"`
		Failed          []json.RawMessage `json:"failed"`
		MediaSideloaded int               `json:"media_sideloaded"`
		Warnings        []string          `json:"warnings"`
		RollbackPath    *string           `json:"rollback_path"`
		Journal         *string           `json:"journal"`
		Error           *string           `json:"error"`
	} `json:"report"`
	Applied []string `json:"applied"`
}

// Journal is one import or publish the site received.
type Journal struct {
	File      string `json:"file"`
	CreatedAt string `json:"created_at"`
	Source    string `json:"source"`
	Counts    struct {
		Updated int `json:"updated"`
		Created int `json:"created"`
		Deleted int `json:"deleted"`
	} `json:"counts"`
	Undone bool `json:"undone"`
}

// UndoResult is what an undo did, or would do (a dry run).
type UndoResult struct {
	Restored  int      `json:"restored"`
	Deleted   int      `json:"deleted"`
	Recreated int      `json:"recreated"`
	Kept      []string `json:"kept"`
	Journal   string   `json:"journal"`
}
