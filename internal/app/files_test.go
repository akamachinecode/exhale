package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/domain"
)

var testPDF = []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\ntrailer\n")

func testImage(t *testing.T, w, h int, enc string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	var b bytes.Buffer
	var err error
	if enc == "jpeg" {
		err = jpeg.Encode(&b, img, nil)
	} else {
		err = png.Encode(&b, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDocumentsAddListDownloadDeleteUndo(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Gym", Price: "39", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20"})
	subID := d.Subscription.ID

	// A dry run checks everything and saves nothing.
	if _, err := f.svc.AddDocument(ctx, f.owner, AddDocumentInput{SubscriptionID: subID, Name: "contract.pdf", Data: testPDF, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if l, _ := f.svc.ListDocuments(ctx, f.owner, ListDocumentsInput{ID: subID}); len(l.Documents) != 0 {
		t.Fatalf("a dry run saved a document: %+v", l)
	}

	contract, err := f.svc.AddDocument(ctx, f.owner, AddDocumentInput{SubscriptionID: subID, Name: `C:\Users\me\Contrato "gym"\n.exe`, Data: testPDF})
	if err != nil {
		t.Fatal(err)
	}
	doc := contract.Document
	if doc.Kind != DocContract || doc.MediaType != "application/pdf" || doc.Size != int64(len(testPDF)) || doc.CreatedBy != f.owner.Label {
		t.Fatalf("document: %+v", doc)
	}
	if strings.ContainsAny(doc.Name, `/\"`+"\n") || !strings.HasSuffix(doc.Name, ".pdf") {
		t.Fatalf("the name must be one safe line with the real extension: %q", doc.Name)
	}
	invoice, err := f.svc.AddDocument(ctx, f.owner, AddDocumentInput{SubscriptionID: subID, ChargeID: d.Upcoming[0].ID, Name: "receipt.jpg", Data: testImage(t, 20, 10, "jpeg")})
	if err != nil {
		t.Fatal(err)
	}
	if invoice.Document.Kind != DocInvoice || invoice.Document.ChargeID != d.Upcoming[0].ID || invoice.Document.MediaType != "image/jpeg" {
		t.Fatalf("invoice: %+v", invoice.Document)
	}
	list, err := f.svc.ListDocuments(ctx, f.owner, ListDocumentsInput{ID: subID})
	if err != nil || len(list.Documents) != 2 {
		t.Fatalf("list: %+v %v", list, err)
	}
	got, data, err := f.svc.DocumentFile(ctx, f.owner, doc.ID)
	if err != nil || !bytes.Equal(data, testPDF) || got.ID != doc.ID {
		t.Fatalf("download: %v", err)
	}

	del, err := f.svc.DeleteDocument(ctx, f.owner, DeleteDocumentInput{ID: doc.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.DocumentFile(ctx, f.owner, doc.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a deleted document must be gone: %v", err)
	}
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditID: del.AuditID}); err != nil {
		t.Fatal(err)
	}
	if _, data, err := f.svc.DocumentFile(ctx, f.owner, doc.ID); err != nil || !bytes.Equal(data, testPDF) {
		t.Fatalf("undo must bring the document back: %v", err)
	}
	// Undo of an upload removes it.
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditID: invoice.AuditID}); err != nil {
		t.Fatal(err)
	}
	if l, _ := f.svc.ListDocuments(ctx, f.owner, ListDocumentsInput{ID: subID}); len(l.Documents) != 1 {
		t.Fatalf("after undoing the upload: %+v", l)
	}
	// Every change is in the audit log.
	log, _ := f.svc.ListAudit(ctx, f.owner, AuditLogInput{})
	actions := []string{}
	for _, e := range log.Entries {
		actions = append(actions, e.Action)
	}
	if s := strings.Join(actions, ","); !strings.Contains(s, "document.add") || !strings.Contains(s, "document.delete") {
		t.Fatalf("audit: %s", s)
	}
}

func TestDocumentsRefuseWhatIsNotAPDFOrAPicture(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Gym", Price: "39", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20"})
	for name, data := range map[string][]byte{
		"html":          []byte("<html><script>alert(1)</script></html>"),
		"svg":           []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"empty":         {},
		"broken png":    []byte("\x89PNG\r\n\x1a\nnot really"),
		"too large pdf": append(append([]byte{}, testPDF...), make([]byte, MaxDocumentSize)...),
	} {
		_, err := f.svc.AddDocument(ctx, f.owner, AddDocumentInput{SubscriptionID: d.Subscription.ID, Name: "x.pdf", Data: data})
		if !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: want a validation error, got %v", name, err)
		}
	}
	// A charge of another subscription cannot carry this one's invoice.
	other := f.create(t, CreateSubscriptionInput{Name: "Netflix", Price: "9", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20"})
	if _, err := f.svc.AddDocument(ctx, f.owner, AddDocumentInput{SubscriptionID: d.Subscription.ID, ChargeID: other.Upcoming[0].ID, Data: testPDF}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("foreign charge: %v", err)
	}
}

func TestDocumentsOfPrivateSubscriptionsStayPrivate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Therapy", Price: "60", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20", Private: true})
	res, err := f.svc.AddDocument(ctx, f.owner, AddDocumentInput{SubscriptionID: d.Subscription.ID, Name: "terms.pdf", Data: testPDF})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetLogo(ctx, f.owner, SetLogoInput{ID: d.Subscription.ID, Data: testImage(t, 32, 32, "png")}); err != nil {
		t.Fatal(err)
	}
	other := Actor{Kind: ActorMember, MemberID: "mem_other", HouseholdID: f.owner.HouseholdID, Label: "Sam", Scope: ScopeWrite}
	if _, err := f.svc.ListDocuments(ctx, other, ListDocumentsInput{ID: d.Subscription.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("list: %v", err)
	}
	if _, _, err := f.svc.DocumentFile(ctx, other, res.Document.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("download: %v", err)
	}
	if _, err := f.svc.DeleteDocument(ctx, other, DeleteDocumentInput{ID: res.Document.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete: %v", err)
	}
	if _, _, err := f.svc.Logo(ctx, other, d.Subscription.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("logo: %v", err)
	}
	exp, err := f.svc.Export(ctx, other, struct{}{})
	if err != nil || len(exp.Subscriptions) != 0 {
		t.Errorf("export: %+v %v", exp.Subscriptions, err)
	}
	// Read-only access cannot attach anything.
	reader := f.owner
	reader.Scope = ScopeRead
	reader.Kind = ActorAgent
	if _, err := f.svc.AddDocument(ctx, reader, AddDocumentInput{SubscriptionID: d.Subscription.ID, Data: testPDF}); !errors.Is(err, ErrForbidden) {
		t.Errorf("read-only: %v", err)
	}
}

func TestLogoIsRedrawnAsASmallPNG(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Local gym", Price: "39", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20"})
	id := d.Subscription.ID

	big := testImage(t, 800, 400, "jpeg")
	res, err := f.svc.SetLogo(ctx, f.owner, SetLogoInput{ID: id, Data: big})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Logo, "/api/v1/subscriptions/"+id+"/logo?v=") {
		t.Fatalf("logo url: %q", res.Logo)
	}
	meta, data, err := f.svc.Logo(ctx, f.owner, id)
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || format != "png" || meta.MediaType != "image/png" {
		t.Fatalf("stored logo: %s %v", format, err)
	}
	if b := img.Bounds(); b.Dx() != LogoSize || b.Dy() != LogoSize/2 {
		t.Fatalf("size: %v", b)
	}
	// Views carry the versioned address, for subscriptions and charges.
	got, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: id})
	if got.Subscription.Logo != res.Logo || got.Upcoming[0].Logo != res.Logo {
		t.Fatalf("views: %q %q", got.Subscription.Logo, got.Upcoming[0].Logo)
	}
	// A new logo gets a new address; undo brings the old one back.
	second, err := f.svc.SetLogo(ctx, f.owner, SetLogoInput{ID: id, Data: testImage(t, 64, 64, "png")})
	if err != nil || second.Logo == res.Logo {
		t.Fatalf("second logo: %+v %v", second, err)
	}
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditID: second.AuditID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: id}); got.Subscription.Logo != res.Logo {
		t.Fatalf("after undo: %q", got.Subscription.Logo)
	}
	// Removing it goes back to the bundled mark or the initials.
	rm, err := f.svc.RemoveLogo(ctx, f.owner, RemoveLogoInput{ID: id})
	if err != nil || rm.Logo != "" {
		t.Fatalf("remove: %+v %v", rm, err)
	}
	if got, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: id}); got.Subscription.Logo != "" {
		t.Fatalf("after remove: %q", got.Subscription.Logo)
	}
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditID: rm.AuditID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: id}); got.Subscription.Logo != res.Logo {
		t.Fatalf("after undoing the removal: %q", got.Subscription.Logo)
	}
}

func TestLogoRefusesSVGAndOddFiles(t *testing.T) {
	for name, data := range map[string][]byte{
		"svg":     []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`),
		"pdf":     testPDF,
		"too big": append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxLogoUpload)...),
	} {
		if _, err := NormalizeLogo(data); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := NormalizeLogo([]byte(`<svg/>`)); err == nil || !strings.Contains(err.Error(), "SVG") {
		t.Errorf("SVG must be named in the error: %v", err)
	}
	// A PNG header claiming a huge picture is refused before decoding it.
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))
	bomb := b.Bytes()
	// IHDR width and height live at bytes 16..23; the CRC no longer
	// matches, so the decoder refuses it either way.
	copy(bomb[16:24], []byte{0, 0, 0x40, 0, 0, 0, 0x40, 0})
	if _, err := NormalizeLogo(bomb); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("huge picture: %v", err)
	}
}

func TestDeletingASubscriptionKeepsItsFilesForUndoThenPurges(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Gym", Price: "39", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20"})
	id := d.Subscription.ID
	doc, err := f.svc.AddDocument(ctx, f.owner, AddDocumentInput{SubscriptionID: id, Name: "c.pdf", Data: testPDF})
	if err != nil {
		t.Fatal(err)
	}
	logo, err := f.svc.SetLogo(ctx, f.owner, SetLogoInput{ID: id, Data: testImage(t, 16, 16, "png")})
	if err != nil {
		t.Fatal(err)
	}
	del, err := f.svc.DeleteSubscription(ctx, f.owner, DeleteSubscriptionInput{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditID: del.AuditID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.DocumentFile(ctx, f.owner, doc.Document.ID); err != nil {
		t.Fatalf("undoing the deletion must bring the document back: %v", err)
	}
	if got, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: id}); got.Subscription.Logo != logo.Logo {
		t.Fatalf("and the logo: %q", got.Subscription.Logo)
	}

	// Deleted again; 30 days later the files are purged for good.
	if _, err := f.svc.DeleteSubscription(ctx, f.owner, DeleteSubscriptionInput{ID: id}); err != nil {
		t.Fatal(err)
	}
	if n, err := f.svc.PurgeDeletedFiles(ctx); err != nil || n != 0 {
		t.Fatalf("too early to purge: %d %v", n, err)
	}
	f.now = f.now.Add(FileRetention + time.Hour)
	if n, err := f.svc.PurgeDeletedFiles(ctx); err != nil || n != 2 { // the document and the logo
		t.Fatalf("purge: %d %v", n, err)
	}
}

func TestExportListsFiles(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Gym", Price: "39", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20"})
	if _, err := f.svc.AddDocument(ctx, f.owner, AddDocumentInput{SubscriptionID: d.Subscription.ID, Name: "c.pdf", Data: testPDF}); err != nil {
		t.Fatal(err)
	}
	exp, err := f.svc.Export(ctx, f.owner, struct{}{})
	if err != nil || len(exp.Subscriptions) != 1 || len(exp.Subscriptions[0].Files) != 1 || exp.Subscriptions[0].Files[0].Name != "c.pdf" {
		t.Fatalf("export: %+v %v", exp.Subscriptions, err)
	}
	data, err := f.svc.FileData(ctx, f.owner, exp.Subscriptions[0].Files[0].ID)
	if err != nil || !bytes.Equal(data, testPDF) {
		t.Fatalf("file data: %v", err)
	}
}
