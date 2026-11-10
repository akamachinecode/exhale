package importer

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// writePDF builds a one-page PDF from a content stream, the way statement
// generators do: compressed content, a standard font, a cross-reference
// table. image adds a picture (for the scanned fixture).
func writePDF(content string, image bool) []byte {
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	_, _ = w.Write([]byte(content))
	_ = w.Close()
	resources := "/Font << /F1 4 0 R >>"
	if image {
		resources = "/XObject << /Im1 6 0 R >>"
	}
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << " + resources + " >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>\nstream\n%s\nendstream", z.Len(), z.String()),
		"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Length 1 >>\nstream\n\x80\nendstream",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// statementRows are the rows of the fixture statement: two months of an
// invented account, no personal data.
var statementRows = [][4]string{
	{"03/09/2026", "RECIBO IBERDROLA COMERCIAL", "-48,10", "951,90"},
	{"06/09/2026", "NETFLIX.COM 866-579-7172", "-12,99", "938,91"},
	{"10/09/2026", "MERCADONA AV DIAGONAL", "-62,53", "876,38"},
	{"15/09/2026", "ADOBE CREATIVE CLOUD", "-67,25", "809,13"},
	{"28/09/2026", "SPOTIFY P6A2B3C4D", "-17,99", "791,14"},
	{"30/09/2026", "NOMINA ACME CORP", "2.450,00", "3.241,14"},
	{"03/10/2026", "RECIBO IBERDROLA COMERCIAL", "-55,40", "3.185,74"},
	{"06/10/2026", "NETFLIX.COM 866-579-7172", "-15,99", "3.169,75"},
	{"12/10/2026", "MERCADONA AV DIAGONAL", "-41,16", "3.128,59"},
	{"15/10/2026", "ADOBE CREATIVE CLOUD", "-67,25", "3.061,34"},
	{"28/10/2026", "SPOTIFY P6A2B3C4D", "-17,99", "3.043,35"},
}

// statementContent draws each cell as its own text object at its column,
// so the reader must put the rows back together.
func statementContent() string {
	var b strings.Builder
	cell := func(x, y float64, s string) {
		fmt.Fprintf(&b, "BT /F1 9 Tf 1 0 0 1 %.0f %.0f Tm (%s) Tj ET\n", x, y, s)
	}
	cell(40, 800, "Example Bank - Account statement")
	cell(40, 785, "Period 01/09/2026 - 31/10/2026")
	cell(40, 760, "Date")
	cell(110, 760, "Description")
	cell(400, 760, "Amount")
	cell(480, 760, "Balance")
	cell(110, 745, "Opening balance")
	cell(480, 745, "1.000,00")
	// Columns are drawn one after the other, as many generators do.
	for col, x := range []float64{40, 110, 400, 480} {
		for i, r := range statementRows {
			cell(x, float64(730-15*i), r[col])
		}
	}
	cell(110, 550, "Closing balance")
	cell(480, 550, "3.043,35")
	return b.String()
}

// TestWriteFixtures regenerates testdata/statement.pdf and scan.pdf.
func TestWriteFixtures(t *testing.T) {
	if os.Getenv("EXHALE_WRITE_FIXTURES") == "" {
		t.Skip("set EXHALE_WRITE_FIXTURES=1 to regenerate the PDF fixtures")
	}
	if err := os.WriteFile("testdata/statement.pdf", writePDF(statementContent(), false), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("testdata/scan.pdf", writePDF("q 500 0 0 700 40 80 cm /Im1 Do Q\n", true), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPDFText(t *testing.T) {
	data, err := os.ReadFile("testdata/statement.pdf")
	if err != nil {
		t.Fatal(err)
	}
	text, err := PDFText(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"06/10/2026  NETFLIX.COM 866-579-7172", "-15,99  3.169,75", "Opening balance"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	res, shaped := TextTransactions(text, "EUR", today, "statement")
	if !shaped {
		t.Fatalf("no transactions in:\n%s", text)
	}
	if len(res.Transactions) != 10 {
		t.Errorf("%d payments, want 10 (the salary and the balances are not payments): %+v", len(res.Transactions), res.Transactions)
	}
	got := byName(res.Items)
	if it, ok := got["Adobe Creative"]; !ok || it.Price != "67.25" || it.NextCharge != "2026-11-15" {
		t.Errorf("Adobe: %+v (all: %v)", it, names(res.Items))
	}
	if it, ok := got["Spotify"]; !ok || it.Price != "17.99" {
		t.Errorf("Spotify: %+v", it)
	}
	if _, ok := got["Mercadona Diagonal"]; ok {
		t.Error("two different shopping amounts are not a subscription")
	}
}

func TestScannedPDF(t *testing.T) {
	data, err := os.ReadFile("testdata/scan.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PDFText(data); !errors.Is(err, ErrNoText) {
		t.Fatalf("a scan: %v", err)
	}
	if _, err := PDFText([]byte("%PDF-1.4\ngarbage")); err == nil || errors.Is(err, ErrNoText) {
		t.Fatalf("a damaged PDF: %v", err)
	}
}

func TestTextLines(t *testing.T) {
	text := `Statement for November
2026-11-03  NETFLIX.COM  12.99 EUR
Nov 6, 2026 Spotify AB 10.99
11/08/2026 GYM URBAN 29.00
11/15/2026 Refund ACME 5.00-
Balance 11/30/2026 1,234.56
`
	res, shaped := TextTransactions(text, "USD", today, "text")
	if !shaped {
		t.Fatal("not read")
	}
	want := map[string]string{"NETFLIX.COM": "2026-11-03 12.99 EUR", "Spotify AB": "2026-11-06 10.99 USD", "GYM URBAN": "2026-11-08 29.00 USD"}
	if len(res.Transactions) != len(want) {
		t.Fatalf("%+v", res.Transactions)
	}
	for _, l := range res.Transactions {
		if w := want[l.Description]; w != l.Date+" "+l.Amount+" "+l.Currency {
			t.Errorf("%s: %s %s %s, want %s", l.Description, l.Date, l.Amount, l.Currency, w)
		}
	}
	if _, shaped := TextTransactions("Hello\nNetflix costs 12.99 a month\n", "EUR", today, "text"); shaped {
		t.Error("prose is not a statement")
	}
}
