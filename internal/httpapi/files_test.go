package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func uploadFile(t *testing.T, url, name string, data []byte, fields map[string]string) (int, map[string]any) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", url, &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// TestDocumentsOverHTTP: upload, list, download with safe headers, the
// full export archive, delete.
func TestDocumentsOverHTTP(t *testing.T) {
	srv, _, _ := setup(t, true)
	_, created := do(t, "POST", srv.URL+"/api/v1/subscriptions", map[string]any{"name": "Gym", "price": "39", "currency": "EUR", "interval": 1, "unit": "month", "firstCharge": "2026-11-20"}, nil)
	id := created["subscription"].(map[string]any)["id"].(string)
	pdf := []byte("%PDF-1.7\n%%EOF\n")

	code, out := uploadFile(t, srv.URL+"/api/v1/subscriptions/"+id+"/documents", "Contrato gimnasio.pdf", pdf, map[string]string{"kind": "contract"})
	if code != 201 {
		t.Fatalf("upload: %d %v", code, out)
	}
	doc := out["document"].(map[string]any)
	if doc["name"] != "Contrato gimnasio.pdf" || doc["kind"] != "contract" {
		t.Fatalf("document: %v", doc)
	}
	code, out = uploadFile(t, srv.URL+"/api/v1/subscriptions/"+id+"/documents", "evil.html", []byte("<script>alert(1)</script>"), nil)
	if code != 422 || !strings.Contains(out["detail"].(string), "PDF") {
		t.Fatalf("html must be refused: %d %v", code, out)
	}

	code, list := do(t, "GET", srv.URL+"/api/v1/subscriptions/"+id+"/documents", nil, nil)
	if code != 200 || len(list["documents"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, list)
	}
	res, err := http.Get(srv.URL + doc["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	h := res.Header
	if res.StatusCode != 200 || !bytes.Equal(body, pdf) {
		t.Fatalf("download: %d", res.StatusCode)
	}
	if h.Get("Content-Type") != "application/pdf" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") ||
		!strings.HasPrefix(h.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download headers: %v", h)
	}

	// The full export carries the file and the JSON that lists it.
	res, err = http.Get(srv.URL + "/api/v1/export/archive.zip")
	if err != nil {
		t.Fatal(err)
	}
	zipped, _ := io.ReadAll(res.Body)
	res.Body.Close()
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	names := map[string]*zip.File{}
	for _, f := range zr.File {
		names[f.Name] = f
	}
	path := "files/" + doc["id"].(string) + "-Contrato gimnasio.pdf"
	if names["exhale-export.json"] == nil || names[path] == nil {
		t.Fatalf("archive files: %v", names)
	}
	rc, _ := names["exhale-export.json"].Open()
	manifest, _ := io.ReadAll(rc)
	rc.Close()
	if !strings.Contains(string(manifest), `"path": "`+path+`"`) {
		t.Fatalf("manifest does not point at the file:\n%s", manifest)
	}

	code, del := do(t, "POST", srv.URL+"/api/v1/documents/delete", map[string]any{"id": doc["id"]}, nil)
	if code != 200 || del["auditId"] == "" {
		t.Fatalf("delete: %d %v", code, del)
	}
	if code, _ := get(t, srv.URL+doc["url"].(string)); code != 404 {
		t.Fatalf("deleted download: %d", code)
	}
}

func TestLogoOverHTTP(t *testing.T) {
	srv, _, _ := setup(t, true)
	_, created := do(t, "POST", srv.URL+"/api/v1/subscriptions", map[string]any{"name": "Corner gym", "price": "39", "currency": "EUR", "interval": 1, "unit": "month", "firstCharge": "2026-11-20"}, nil)
	id := created["subscription"].(map[string]any)["id"].(string)

	code, out := uploadFile(t, srv.URL+"/api/v1/subscriptions/"+id+"/logo", "logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), nil)
	if code != 422 || !strings.Contains(out["detail"].(string), "SVG") {
		t.Fatalf("svg: %d %v", code, out)
	}
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 40, 40)))
	code, out = uploadFile(t, srv.URL+"/api/v1/subscriptions/"+id+"/logo", "logo.png", b.Bytes(), nil)
	if code != 200 {
		t.Fatalf("upload: %d %v", code, out)
	}
	url := out["logo"].(string)
	_, got := do(t, "GET", srv.URL+"/api/v1/subscriptions/"+id, nil, nil)
	if got["subscription"].(map[string]any)["logo"] != url {
		t.Fatalf("view: %v", got["subscription"])
	}
	res, err := http.Get(srv.URL + url)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	h := res.Header
	if res.StatusCode != 200 || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || !strings.Contains(h.Get("Cache-Control"), "immutable") {
		t.Fatalf("logo: %d %v", res.StatusCode, h)
	}
	// Without the version it is not cached for good.
	res, _ = http.Get(srv.URL + "/api/v1/subscriptions/" + id + "/logo")
	res.Body.Close()
	if strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("unversioned logo cached for good: %v", res.Header)
	}

	code, rm := do(t, "POST", srv.URL+"/api/v1/subscriptions/logo/remove", map[string]any{"id": id}, nil)
	if code != 200 || rm["logo"] != nil {
		t.Fatalf("remove: %d %v", code, rm)
	}
	if code, _ := get(t, srv.URL+url); code != 404 {
		t.Fatalf("removed logo: %d", code)
	}
}

func TestReadTermsNeedsTheAssistant(t *testing.T) {
	srv, _, _ := setup(t, true)
	_, created := do(t, "POST", srv.URL+"/api/v1/subscriptions", map[string]any{"name": "Gym", "price": "39", "currency": "EUR", "interval": 1, "unit": "month", "firstCharge": "2026-11-20"}, nil)
	id := created["subscription"].(map[string]any)["id"].(string)
	_, out := uploadFile(t, srv.URL+"/api/v1/subscriptions/"+id+"/documents", "c.pdf", []byte("%PDF-1.7\n"), nil)
	docID := out["document"].(map[string]any)["id"].(string)
	code, res := do(t, "POST", srv.URL+"/api/v1/assistant/documents/"+docID+"/terms", nil, nil)
	if code != 409 {
		t.Fatalf("with the assistant off: %d %v", code, res)
	}
}
