package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	_ "image/jpeg" // decode uploaded logos
	"image/png"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode uploaded logos

	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Files people attach to a subscription (ADR 0027): its own logo, and
// documents such as a contract or an invoice. They live in the database,
// so the data folder, every backup and a restore hold them too.

// Limits of files.
const (
	// MaxDocumentSize is the largest document one upload can add.
	MaxDocumentSize = 10 << 20
	// MaxLogoUpload is the largest picture accepted as a logo. It is
	// re-encoded to a small PNG before it is stored.
	MaxLogoUpload = 2 << 20
	// MaxHouseholdFiles bounds what one household keeps, deleted files of
	// the last 30 days included.
	MaxHouseholdFiles = 500 << 20
	// LogoSize is the side of the square a logo is fitted into.
	LogoSize = 256
	// maxLogoPixels refuses pictures that would take a lot of memory to
	// decode (a small file can claim a huge size).
	maxLogoPixels = 40_000_000
	// FileRetention is how long a deleted file is kept, so undo can bring it
	// back.
	FileRetention = 30 * 24 * time.Hour
)

// Document kinds. A logo is stored the same way but is not a document.
const (
	DocContract = "contract"
	DocInvoice  = "invoice"
	DocOther    = "other"
	kindLogo    = "logo"
)

// Document is a file attached to a subscription, without its content.
type Document struct {
	ID             string    `json:"id"`
	SubscriptionID string    `json:"subscriptionId"`
	ChargeID       string    `json:"chargeId,omitempty" jsonschema:"the charge this invoice or receipt belongs to, if any"`
	Kind           string    `json:"kind" jsonschema:"contract (contract or terms), invoice (invoice or receipt), other; logo in an export" enum:"contract,invoice,other,logo"`
	Name           string    `json:"name" jsonschema:"file name as uploaded"`
	MediaType      string    `json:"mediaType" jsonschema:"application/pdf, image/png, image/jpeg or image/webp"`
	Size           int64     `json:"size" jsonschema:"bytes"`
	SHA256         string    `json:"sha256"`
	CreatedAt      time.Time `json:"createdAt"`
	CreatedBy      string    `json:"createdBy" jsonschema:"who attached it"`
	// URL is where the content is downloaded, with the caller's credentials.
	URL string `json:"url" jsonschema:"path to download the file, under this Exhale's address"`
	// Path is the file inside a full export archive; empty elsewhere.
	Path string `json:"path,omitempty"`
}

func documentFromRow(r db.GetFileMetaRow) Document {
	d := Document{
		ID: r.ID, SubscriptionID: r.SubscriptionID, ChargeID: r.ChargeID, Kind: r.Kind, Name: r.Name, MediaType: r.MediaType,
		Size: r.Size, SHA256: r.Sha256, CreatedAt: store.ParseTime(r.CreatedAt), CreatedBy: r.CreatedBy,
	}
	if d.Kind == kindLogo {
		d.URL = logoURL(r.SubscriptionID, r.Sha256)
	} else {
		d.URL = "/api/v1/documents/" + r.ID + "/file"
	}
	return d
}

// logoURL carries a version, so browsers can keep a logo for good and
// still see a new one at once.
func logoURL(subID, sum string) string {
	return "/api/v1/subscriptions/" + subID + "/logo?v=" + sum[:min(len(sum), 12)]
}

// --- documents ----------------------------------------------------------------------

// AddDocumentInput attaches a file. Uploads come over HTTP (multipart); the
// content is not part of the op catalog.
type AddDocumentInput struct {
	SubscriptionID string
	ChargeID       string
	Kind           string
	Name           string
	Data           []byte
	DryRun         bool
}

// DocumentResult is a document after a change, with the audit id for undo.
type DocumentResult struct {
	Document Document `json:"document"`
	AuditID  string   `json:"auditId,omitempty" jsonschema:"pass to undo to revert"`
}

// AddDocument attaches a PDF or a picture to a subscription, or to one of
// its charges (an invoice or a receipt).
func (s *Service) AddDocument(ctx context.Context, actor Actor, in AddDocumentInput) (DocumentResult, error) {
	media, err := documentType(in.Data)
	if err != nil {
		return DocumentResult{}, err
	}
	switch in.Kind {
	case "":
		in.Kind = DocContract
		if in.ChargeID != "" {
			in.Kind = DocInvoice
		}
	case DocContract, DocInvoice, DocOther:
	default:
		return DocumentResult{}, domain.Invalid("kind must be contract, invoice or other")
	}
	var out DocumentResult
	err = s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		sub, err := s.subscription(ctx, q, actor, in.SubscriptionID)
		if err != nil {
			return err
		}
		if in.ChargeID != "" {
			_, chSub, err := s.charge(ctx, q, actor, in.ChargeID)
			if err != nil {
				return err
			}
			if chSub.ID != sub.ID {
				return domain.Invalid("charge %s is not one of %s", in.ChargeID, sub.Name)
			}
		}
		meta, err := s.storeFile(ctx, q, c, actor, sub, in.ChargeID, in.Kind, cleanFileName(in.Name, media), media, in.Data)
		if err != nil {
			return err
		}
		id, err := c.record(q, "document.add", "document", meta.ID, nil, meta)
		if err != nil {
			return err
		}
		out = DocumentResult{Document: meta, AuditID: id}
		c.emit(bus.Event{Type: bus.SubscriptionUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID, EntityID: meta.ID})
		return nil
	})
	return out, err
}

// documentType tells a PDF or a picture from its first bytes. The name and
// the type the browser sent are not trusted.
func documentType(data []byte) (string, error) {
	if len(data) == 0 {
		return "", domain.Invalid("the file is empty")
	}
	if len(data) > MaxDocumentSize {
		return "", domain.Invalid("the file is larger than %d MB", MaxDocumentSize>>20)
	}
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		return "application/pdf", nil
	}
	if media := imageType(data); media != "" {
		if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
			return "", domain.Invalid("the picture cannot be read")
		}
		return media, nil
	}
	return "", domain.Invalid("attach a PDF or a picture (PNG, JPEG or WebP)")
}

func imageType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

var extFor = map[string]string{"application/pdf": ".pdf", "image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp"}

// cleanFileName keeps a name people recognise but safe to show and to put
// in a download header: one line, no path, no control characters, at most
// 120 characters, with an extension that matches the content.
func cleanFileName(name, media string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = path.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' || r == '/' || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	ext := extFor[media]
	base := strings.TrimSuffix(name, path.Ext(name))
	if base == "" || base == "." || base == ".." {
		base = "document"
	}
	if r := []rune(base); len(r) > 120-len(ext) {
		base = string(r[:120-len(ext)])
	}
	return base + ext
}

// storeFile checks the household's space and writes the file.
func (s *Service) storeFile(ctx context.Context, q *db.Queries, c *change, actor Actor, sub domain.Subscription, chargeID, kind, name, media string, data []byte) (Document, error) {
	used, err := q.HouseholdFilesSize(ctx, sub.HouseholdID)
	if err != nil {
		return Document{}, err
	}
	if used+int64(len(data)) > MaxHouseholdFiles {
		return Document{}, domain.Invalid("this Exhale keeps up to %d MB of files and that is full. Deleted files count for 30 days", MaxHouseholdFiles>>20)
	}
	sum := sha256.Sum256(data)
	row := db.GetFileMetaRow{
		ID: domain.NewID("doc"), HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID, ChargeID: chargeID, Kind: kind, Name: name,
		MediaType: media, Size: int64(len(data)), Sha256: hex.EncodeToString(sum[:]), CreatedAt: store.FormatTime(c.now), CreatedBy: actor.Label,
	}
	if err := q.CreateFile(ctx, db.CreateFileParams{
		ID: row.ID, HouseholdID: row.HouseholdID, SubscriptionID: row.SubscriptionID, ChargeID: row.ChargeID, Kind: row.Kind, Name: row.Name,
		MediaType: row.MediaType, Size: row.Size, Sha256: row.Sha256, CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, Data: data,
	}); err != nil {
		return Document{}, err
	}
	return documentFromRow(row), nil
}

// ListDocumentsInput names the subscription.
type ListDocumentsInput struct {
	ID string `json:"id" path:"id" jsonschema:"subscription id (sub_…)"`
}

// DocumentList is the documents of one subscription, newest first.
type DocumentList struct {
	Documents []Document `json:"documents"`
}

// ListDocuments returns the documents attached to a subscription the actor
// can see.
func (s *Service) ListDocuments(ctx context.Context, actor Actor, in ListDocumentsInput) (DocumentList, error) {
	sub, err := s.subscription(ctx, s.st.Q, actor, in.ID)
	if err != nil {
		return DocumentList{}, err
	}
	rows, err := s.st.Q.ListDocuments(ctx, db.ListDocumentsParams{SubscriptionID: sub.ID, HouseholdID: sub.HouseholdID})
	if err != nil {
		return DocumentList{}, err
	}
	out := DocumentList{Documents: make([]Document, 0, len(rows))}
	for _, r := range rows {
		out.Documents = append(out.Documents, documentFromRow(db.GetFileMetaRow(r)))
	}
	return out, nil
}

// file loads the metadata of a live file whose subscription the actor can
// see. Anything else is "not found", so ids of private files leak nothing.
func (s *Service) file(ctx context.Context, q *db.Queries, actor Actor, id string) (Document, domain.Subscription, error) {
	r, err := q.GetFileMeta(ctx, db.GetFileMetaParams{ID: id, HouseholdID: actor.HouseholdID})
	if err != nil {
		return Document{}, domain.Subscription{}, store.NotFound(err, "document %s: not found", id)
	}
	if r.DeletedAt != "" {
		return Document{}, domain.Subscription{}, domain.Missing("document %s: not found", id)
	}
	sub, err := s.subscription(ctx, q, actor, r.SubscriptionID)
	if err != nil {
		return Document{}, domain.Subscription{}, domain.Missing("document %s: not found", id)
	}
	return documentFromRow(r), sub, nil
}

// DocumentFile returns a document and its content.
func (s *Service) DocumentFile(ctx context.Context, actor Actor, id string) (Document, []byte, error) {
	d, _, err := s.file(ctx, s.st.Q, actor, id)
	if err != nil {
		return d, nil, err
	}
	if d.Kind == kindLogo {
		return Document{}, nil, domain.Missing("document %s: not found", id)
	}
	data, err := s.st.Q.GetFileData(ctx, db.GetFileDataParams{ID: id, HouseholdID: actor.HouseholdID})
	return d, data, store.NotFound(err, "document %s: not found", id)
}

// DeleteDocumentInput names the document.
type DeleteDocumentInput struct {
	ID     string `json:"id" jsonschema:"document id (doc_…)"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// DeleteDocument removes a document. It is kept 30 days, so undo can bring
// it back, then purged.
func (s *Service) DeleteDocument(ctx context.Context, actor Actor, in DeleteDocumentInput) (DocumentResult, error) {
	var out DocumentResult
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		d, sub, err := s.file(ctx, q, actor, in.ID)
		if err != nil {
			return err
		}
		if d.Kind == kindLogo {
			return domain.Missing("document %s: not found", in.ID)
		}
		if err := q.TrashFile(ctx, db.TrashFileParams{DeletedAt: store.FormatTime(c.now), ID: d.ID, HouseholdID: sub.HouseholdID}); err != nil {
			return err
		}
		id, err := c.record(q, "document.delete", "document", d.ID, d, nil)
		if err != nil {
			return err
		}
		out = DocumentResult{Document: d, AuditID: id}
		c.emit(bus.Event{Type: bus.SubscriptionUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID, EntityID: d.ID})
		return nil
	})
	return out, err
}

// --- logos ------------------------------------------------------------------------

// SetLogoInput replaces a subscription's logo with an uploaded picture.
type SetLogoInput struct {
	ID     string
	Data   []byte
	DryRun bool
}

// LogoResult is the logo after a change.
type LogoResult struct {
	SubscriptionID string `json:"subscriptionId"`
	Logo           string `json:"logo,omitempty" jsonschema:"path of the logo now shown; empty: the bundled brand mark or the monogram"`
	AuditID        string `json:"auditId,omitempty" jsonschema:"pass to undo to revert"`
}

// SetLogo makes an uploaded picture the subscription's logo. The picture is
// decoded and drawn again as a PNG of at most 256×256: whatever else the
// file carried (metadata, a script, a second format hidden inside) is not
// kept. SVG is refused (ADR 0027).
func (s *Service) SetLogo(ctx context.Context, actor Actor, in SetLogoInput) (LogoResult, error) {
	data, err := NormalizeLogo(in.Data)
	if err != nil {
		return LogoResult{}, err
	}
	var out LogoResult
	err = s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		sub, err := s.subscription(ctx, q, actor, in.ID)
		if err != nil {
			return err
		}
		var before any
		if old, err := q.GetLogo(ctx, db.GetLogoParams{SubscriptionID: sub.ID, HouseholdID: sub.HouseholdID}); err == nil {
			before = documentFromRow(db.GetFileMetaRow(old))
			if err := q.TrashFile(ctx, db.TrashFileParams{DeletedAt: store.FormatTime(c.now), ID: old.ID, HouseholdID: sub.HouseholdID}); err != nil {
				return err
			}
		}
		meta, err := s.storeFile(ctx, q, c, actor, sub, "", kindLogo, "logo.png", "image/png", data)
		if err != nil {
			return err
		}
		id, err := c.record(q, "logo.set", "logo", sub.ID, before, meta)
		if err != nil {
			return err
		}
		out = LogoResult{SubscriptionID: sub.ID, Logo: meta.URL, AuditID: id}
		c.emit(bus.Event{Type: bus.SubscriptionUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID, EntityID: meta.ID})
		return nil
	})
	return out, err
}

// NormalizeLogo decodes a PNG, JPEG or WebP picture and returns it as a PNG
// that fits in LogoSize×LogoSize, keeping its proportions.
func NormalizeLogo(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, domain.Invalid("the file is empty")
	}
	if len(data) > MaxLogoUpload {
		return nil, domain.Invalid("a logo can be up to %d MB", MaxLogoUpload>>20)
	}
	if imageType(data) == "" {
		if looksLikeSVG(data) {
			return nil, domain.Invalid("SVG logos are not accepted, because an SVG can carry a script. Upload a PNG, JPEG or WebP")
		}
		return nil, domain.Invalid("upload a PNG, JPEG or WebP picture")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, domain.Invalid("the picture cannot be read")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxLogoPixels {
		return nil, domain.Invalid("the picture is too large (%d×%d pixels)", cfg.Width, cfg.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, domain.Invalid("the picture cannot be read")
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > LogoSize || h > LogoSize {
		if w >= h {
			w, h = LogoSize, max(1, h*LogoSize/w)
		} else {
			w, h = max(1, w*LogoSize/h), LogoSize
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h)) // transparent
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func looksLikeSVG(data []byte) bool {
	head := strings.ToLower(string(data[:min(len(data), 1024)]))
	return strings.Contains(head, "<svg") || strings.HasPrefix(strings.TrimSpace(head), "<?xml")
}

// RemoveLogoInput names the subscription.
type RemoveLogoInput struct {
	ID     string `json:"id" jsonschema:"subscription id (sub_…)"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// RemoveLogo drops an uploaded logo: the subscription shows the bundled
// brand mark or its monogram again.
func (s *Service) RemoveLogo(ctx context.Context, actor Actor, in RemoveLogoInput) (LogoResult, error) {
	var out LogoResult
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		sub, err := s.subscription(ctx, q, actor, in.ID)
		if err != nil {
			return err
		}
		out.SubscriptionID = sub.ID
		old, err := q.GetLogo(ctx, db.GetLogoParams{SubscriptionID: sub.ID, HouseholdID: sub.HouseholdID})
		if err != nil {
			return nil // nothing to remove
		}
		if err := q.TrashFile(ctx, db.TrashFileParams{DeletedAt: store.FormatTime(c.now), ID: old.ID, HouseholdID: sub.HouseholdID}); err != nil {
			return err
		}
		id, err := c.record(q, "logo.remove", "logo", sub.ID, documentFromRow(db.GetFileMetaRow(old)), nil)
		if err != nil {
			return err
		}
		out.AuditID = id
		c.emit(bus.Event{Type: bus.SubscriptionUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID, EntityID: old.ID})
		return nil
	})
	return out, err
}

// Logo returns the uploaded logo of a subscription the actor can see.
func (s *Service) Logo(ctx context.Context, actor Actor, subscriptionID string) (Document, []byte, error) {
	sub, err := s.subscription(ctx, s.st.Q, actor, subscriptionID)
	if err != nil {
		return Document{}, nil, err
	}
	r, err := s.st.Q.GetLogo(ctx, db.GetLogoParams{SubscriptionID: sub.ID, HouseholdID: sub.HouseholdID})
	if err != nil {
		return Document{}, nil, store.NotFound(err, "logo of %s: not found", sub.ID)
	}
	data, err := s.st.Q.GetFileData(ctx, db.GetFileDataParams{ID: r.ID, HouseholdID: sub.HouseholdID})
	return documentFromRow(db.GetFileMetaRow(r)), data, store.NotFound(err, "logo of %s: not found", sub.ID)
}

// logoOf is the path of a subscription's uploaded logo, or "".
func logoOf(ctx context.Context, q *db.Queries, sub domain.Subscription) string {
	r, err := q.GetLogo(ctx, db.GetLogoParams{SubscriptionID: sub.ID, HouseholdID: sub.HouseholdID})
	if err != nil {
		return ""
	}
	return logoURL(sub.ID, r.Sha256)
}

// --- undo, deletion, purge ---------------------------------------------------------

// undoFile reverts a change to a document or a logo. Deleted files are kept
// for FileRetention; after that the change cannot be undone.
func (s *Service) undoFile(ctx context.Context, q *db.Queries, actor Actor, c *change, a db.AuditLog) error {
	var before, after *Document
	if err := unmarshalDoc(a.Before, &before); err != nil {
		return err
	}
	if err := unmarshalDoc(a.After, &after); err != nil {
		return err
	}
	subID := a.EntityID
	if a.Entity == "document" {
		ref := before
		if ref == nil {
			ref = after
		}
		if ref == nil {
			return domain.Invalid("change %s cannot be undone", a.ID)
		}
		subID = ref.SubscriptionID
	}
	sub, err := s.subscription(ctx, q, actor, subID)
	if err != nil {
		return err
	}
	now := store.FormatTime(c.now)
	if a.Entity == "logo" {
		// Whatever logo shows now gives way to the one before the change.
		if cur, err := q.GetLogo(ctx, db.GetLogoParams{SubscriptionID: sub.ID, HouseholdID: sub.HouseholdID}); err == nil {
			if err := q.TrashFile(ctx, db.TrashFileParams{DeletedAt: now, ID: cur.ID, HouseholdID: sub.HouseholdID}); err != nil {
				return err
			}
		}
	} else if after != nil {
		if err := q.TrashFile(ctx, db.TrashFileParams{DeletedAt: now, ID: after.ID, HouseholdID: sub.HouseholdID}); err != nil {
			return err
		}
	}
	if before != nil {
		if _, err := q.GetFileMeta(ctx, db.GetFileMetaParams{ID: before.ID, HouseholdID: sub.HouseholdID}); err != nil {
			return domain.Invalid("the file was deleted more than 30 days ago and is gone for good")
		}
		if err := q.RestoreFile(ctx, db.RestoreFileParams{ID: before.ID, HouseholdID: sub.HouseholdID}); err != nil {
			return err
		}
	}
	c.emit(bus.Event{Type: bus.SubscriptionUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID})
	return nil
}

func unmarshalDoc(raw string, dst **Document) error {
	if raw == "" || raw == "null" {
		return nil
	}
	var d Document
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return err
	}
	*dst = &d
	return nil
}

// trashFilesOf sets the files of a subscription being deleted aside and
// returns their ids, so undoing the deletion brings them back.
func trashFilesOf(ctx context.Context, q *db.Queries, sub domain.Subscription, now time.Time) ([]string, error) {
	return q.TrashSubscriptionFiles(ctx, db.TrashSubscriptionFilesParams{DeletedAt: store.FormatTime(now), SubscriptionID: sub.ID, HouseholdID: sub.HouseholdID})
}

func restoreFiles(ctx context.Context, q *db.Queries, householdID string, ids []string) error {
	for _, id := range ids {
		if err := q.RestoreFile(ctx, db.RestoreFileParams{ID: id, HouseholdID: householdID}); err != nil {
			return err
		}
	}
	return nil
}

// PurgeDeletedFiles removes for good the files deleted more than
// FileRetention ago. The scheduler calls it every hour.
func (s *Service) PurgeDeletedFiles(ctx context.Context) (int64, error) {
	return s.st.Q.PurgeFiles(ctx, store.FormatTime(s.now().Add(-FileRetention)))
}

// FileData returns the content of a live file (a document or a logo) of a
// subscription the actor can see, for the full export.
func (s *Service) FileData(ctx context.Context, actor Actor, id string) ([]byte, error) {
	if _, _, err := s.file(ctx, s.st.Q, actor, id); err != nil {
		return nil, err
	}
	data, err := s.st.Q.GetFileData(ctx, db.GetFileDataParams{ID: id, HouseholdID: actor.HouseholdID})
	return data, store.NotFound(err, "file %s: not found", id)
}
