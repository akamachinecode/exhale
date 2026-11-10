package importer

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/ledongthuc/pdf"
)

// A statement in PDF is usually text drawn at positions: no lines, no
// table. PDFText puts the pieces back into lines (same height on the page,
// left to right), so the transaction reader can take one per line. A
// scanned PDF has pictures and no text: only a model can read it.

// MaxPDFPages bounds how much of a PDF is read.
const MaxPDFPages = 60

// ErrNoText is returned for a PDF without text, such as a scan.
var ErrNoText = errors.New("this PDF has no text (it is probably a scan)")

// PDFText returns the text of a PDF, one line per line of the page. It
// fails for a damaged or password-protected file and returns ErrNoText for
// a PDF without text.
func PDFText(data []byte) (text string, err error) {
	// The reader panics on some damaged files; a bad upload must not take
	// the server down.
	defer func() {
		if r := recover(); r != nil {
			text, err = "", fmt.Errorf("%w (the PDF could not be read)", ErrUnknownFormat)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("%w (the PDF could not be read: it may be damaged or protected with a password)", ErrUnknownFormat)
	}
	var b strings.Builder
	for i := 1; i <= min(r.NumPage(), MaxPDFPages); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		for _, l := range pageLines(p.Content().Text) {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return "", ErrNoText
	}
	return b.String(), nil
}

// pageLines groups the glyphs of a page into lines: glyphs whose baselines
// are within a third of the font size are on the same line. A gap wider
// than a space becomes a space, a wide one (a new column) two.
func pageLines(glyphs []pdf.Text) []string {
	glyphs = slices.DeleteFunc(slices.Clone(glyphs), func(t pdf.Text) bool { return strings.TrimSpace(t.S) == "" && t.S != " " })
	// Top of the page first; glyphs of one line end up next to each other.
	slices.SortStableFunc(glyphs, func(a, b pdf.Text) int {
		switch {
		case a.Y > b.Y:
			return -1
		case a.Y < b.Y:
			return 1
		}
		return 0
	})
	var lines [][]pdf.Text
	for _, g := range glyphs {
		if n := len(lines); n > 0 && math.Abs(lines[n-1][0].Y-g.Y) <= tolerance(lines[n-1][0], g) {
			lines[n-1] = append(lines[n-1], g)
			continue
		}
		lines = append(lines, []pdf.Text{g})
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		slices.SortStableFunc(l, func(a, b pdf.Text) int {
			switch {
			case a.X < b.X:
				return -1
			case a.X > b.X:
				return 1
			}
			return 0
		})
		var b strings.Builder
		end := l[0].X
		for i, g := range l {
			size := max(g.FontSize, 1)
			if gap := g.X - end; i > 0 && gap > size*0.2 {
				b.WriteString(" ")
				if gap > size*1.5 {
					b.WriteString(" ")
				}
			}
			b.WriteString(g.S)
			w := g.W
			if w <= 0 {
				w = size * 0.5 // fonts without widths: an average glyph
			}
			end = g.X + w
		}
		if s := strings.TrimRight(b.String(), " "); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func tolerance(a, b pdf.Text) float64 {
	return max(a.FontSize, b.FontSize, 1) / 3
}
