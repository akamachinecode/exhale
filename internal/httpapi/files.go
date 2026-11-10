package httpapi

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/assistant"
	"github.com/akamachinecode/exhale/internal/domain"
)

// Logos and documents (ADR 0027). Uploading and downloading carry files,
// not JSON, so they are plain HTTP endpoints over the app's use cases;
// listing and deleting are in the op catalog (list_documents,
// delete_document, remove_logo).

// fileCSP keeps a file from doing anything if a browser ever renders it
// from Exhale's address: no scripts, no requests, a sandbox of its own.
const fileCSP = "default-src 'none'; sandbox"

// AssistantTerms is what a document says about a subscription's contract.
type AssistantTerms assistant.TermsProposal

func registerFiles(api huma.API, mux *http.ServeMux, svc *app.Service, asst *assistant.Assistant) {
	mux.HandleFunc("POST /api/v1/subscriptions/{id}/documents", uploadDocument(svc))
	mux.HandleFunc("GET /api/v1/documents/{id}/file", downloadDocument(svc))
	mux.HandleFunc("POST /api/v1/subscriptions/{id}/logo", uploadLogo(svc))
	mux.HandleFunc("GET /api/v1/subscriptions/{id}/logo", serveLogo(svc))
	mux.HandleFunc("GET /api/v1/export/archive.zip", exportArchive(svc))

	huma.Register(api, huma.Operation{
		OperationID: "assistant_read_terms", Method: http.MethodPost, Path: "/api/v1/assistant/documents/{id}/terms",
		Summary: "Read contract details from an attached document", Tags: []string{"Assistant"},
		Description: "The built-in assistant reads the document and proposes notice days, cancel link, start date, trial end and account. Nothing is saved: apply what you keep with update_subscription.",
	}, func(ctx context.Context, in *struct {
		ID string `path:"id"`
	}) (*resp[AssistantTerms], error) {
		actor, ok := actorFrom(ctx)
		if !ok || actor.Kind != app.ActorMember {
			return nil, huma.Error401Unauthorized("sign in first")
		}
		out, err := asst.ReadTerms(ctx, actor, in.ID)
		if err != nil {
			return nil, assistantError(err)
		}
		return &resp[AssistantTerms]{Body: AssistantTerms(out)}, nil
	})
}

// fileProblem writes the problem for an error of a use case.
func fileProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrValidation):
		writeProblem(w, http.StatusUnprocessableEntity, humanize(err, domain.ErrValidation))
	case errors.Is(err, domain.ErrNotFound):
		writeProblem(w, http.StatusNotFound, humanize(err, domain.ErrNotFound))
	case errors.Is(err, app.ErrForbidden):
		writeProblem(w, http.StatusForbidden, humanize(err, app.ErrForbidden))
	default:
		writeProblem(w, http.StatusInternalServerError, "internal error")
	}
}

// readUpload reads the multipart field "file", refusing more than limit
// bytes without reading them all.
func readUpload(w http.ResponseWriter, r *http.Request, limit int64) (data []byte, name string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)
	f, fh, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeProblem(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the file is larger than %d MB", limit>>20))
			return nil, "", false
		}
		writeProblem(w, http.StatusBadRequest, "attach the file in a field named file")
		return nil, "", false
	}
	defer f.Close()
	data, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "the file could not be read")
		return nil, "", false
	}
	if int64(len(data)) > limit {
		writeProblem(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the file is larger than %d MB", limit>>20))
		return nil, "", false
	}
	return data, fh.Filename, true
}

func uploadDocument(svc *app.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFrom(r.Context())
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "sign in or send a bearer token")
			return
		}
		data, name, ok := readUpload(w, r, app.MaxDocumentSize)
		if !ok {
			return
		}
		dry, _ := strconv.ParseBool(r.FormValue("dryRun"))
		out, err := svc.AddDocument(r.Context(), actor, app.AddDocumentInput{
			SubscriptionID: r.PathValue("id"), ChargeID: strings.TrimSpace(r.FormValue("chargeId")), Kind: r.FormValue("kind"),
			Name: name, Data: data, DryRun: dry,
		})
		if err != nil {
			fileProblem(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	}
}

// fileHeaders are sent with every file Exhale serves back: the type it was
// checked to be, no sniffing, no scripts, not embeddable by other sites.
func fileHeaders(w http.ResponseWriter, media, disposition, name string) {
	h := w.Header()
	h.Set("Content-Type", media)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", fileCSP)
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
}

func downloadDocument(svc *app.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFrom(r.Context())
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "sign in or send a bearer token")
			return
		}
		d, data, err := svc.DocumentFile(r.Context(), actor, r.PathValue("id"))
		if err != nil {
			fileProblem(w, err)
			return
		}
		// Always a download: a document is never shown as a page of Exhale.
		fileHeaders(w, d.MediaType, "attachment", d.Name)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data) //nolint:gosec // a checked PDF or picture, sent as a download with nosniff and a sandbox CSP
	}
}

func uploadLogo(svc *app.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFrom(r.Context())
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "sign in or send a bearer token")
			return
		}
		data, _, ok := readUpload(w, r, app.MaxLogoUpload)
		if !ok {
			return
		}
		dry, _ := strconv.ParseBool(r.FormValue("dryRun"))
		out, err := svc.SetLogo(r.Context(), actor, app.SetLogoInput{ID: r.PathValue("id"), Data: data, DryRun: dry})
		if err != nil {
			fileProblem(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func serveLogo(svc *app.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFrom(r.Context())
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "sign in or send a bearer token")
			return
		}
		d, data, err := svc.Logo(r.Context(), actor, r.PathValue("id"))
		if err != nil {
			fileProblem(w, err)
			return
		}
		fileHeaders(w, "image/png", "inline", "logo.png")
		// The address carries the version: a new logo has a new address,
		// so this one can be kept for good. Another version (an old link)
		// gets the current logo, not cached.
		if v := r.URL.Query().Get("v"); v != "" && strings.HasPrefix(d.SHA256, v) && len(v) >= 8 {
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "private, no-cache")
		}
		w.Header().Set("ETag", `"`+d.SHA256[:16]+`"`)
		if r.Header.Get("If-None-Match") == `"`+d.SHA256[:16]+`"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data) //nolint:gosec // a PNG Exhale drew itself, sent with nosniff and a sandbox CSP
	}
}

// exportArchive is the full export: exhale-export.json (the same document
// as export_data) and every logo and document it lists, in one ZIP.
func exportArchive(svc *app.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFrom(r.Context())
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "sign in or send a bearer token")
			return
		}
		data, err := svc.Export(r.Context(), actor, struct{}{})
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "could not export")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="exhale-export-%s.zip"`, data.ExportedAt.Format(time.DateOnly)))
		zw := zip.NewWriter(w)
		defer func() { _ = zw.Close() }() // the response has started: nothing else to tell
		for i := range data.Subscriptions {
			for j := range data.Subscriptions[i].Files {
				f := &data.Subscriptions[i].Files[j]
				content, err := svc.FileData(r.Context(), actor, f.ID)
				if err != nil {
					continue // deleted while exporting
				}
				f.Path = "files/" + f.ID + "-" + f.Name
				fw, err := zw.CreateHeader(&zip.FileHeader{Name: f.Path, Method: zip.Store, Modified: f.CreatedAt})
				if err != nil {
					return
				}
				if _, err := fw.Write(content); err != nil {
					return
				}
			}
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: "exhale-export.json", Method: zip.Deflate, Modified: data.ExportedAt})
		if err != nil {
			return
		}
		enc := json.NewEncoder(fw)
		enc.SetIndent("", "  ")
		_ = enc.Encode(data)
	}
}
