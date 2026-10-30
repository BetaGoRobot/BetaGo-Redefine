package media

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type countingReader struct{ read int }

func (r *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.read += len(p)
	return len(p), nil
}
func TestDownloadLimitStopsBeforeFullInput(t *testing.T) {
	r := &countingReader{}
	_, err := boundedRead(r, 64)
	if err == nil || r.read > 65 {
		t.Fatalf("input consumed %d %v", r.read, err)
	}
}
func TestOfficeRejectsZipBombAndPreservesSlidePage(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	z, _ := w.Create("ppt/slides/slide2.xml")
	io.WriteString(z, `<a><t>second slide</t></a>`)
	w.Close()
	parts, err := extractOffice(b.Bytes(), Limits{MaxZipBytes: 1024, MaxZipEntries: 5, MaxTextBytes: 1024})
	if err != nil || len(parts) != 1 || parts[0].Page != 2 || parts[0].Text != "second slide" {
		t.Fatalf("pages %v %v", parts, err)
	}
	b.Reset()
	w = zip.NewWriter(&b)
	z, _ = w.Create("word/document.xml")
	io.WriteString(z, strings.Repeat("a", 10000))
	w.Close()
	if _, err = extractOffice(b.Bytes(), Limits{MaxZipBytes: 64, MaxZipEntries: 5, MaxTextBytes: 64}); err == nil {
		t.Fatal("zip bomb accepted")
	}
}
func TestSubprocessOutputAndDeadlineBounded(t *testing.T) {
	if _, err := run(context.Background(), time.Second, 64, "/bin/sh", "-c", "while true; do printf 1234567890; done"); err == nil {
		t.Fatal("unbounded subprocess")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := run(ctx, time.Second, 64, "/bin/sh", "-c", "sleep 5"); err == nil || time.Since(start) > time.Second {
		t.Fatal("subprocess cancellation")
	}
}

type down struct{ data []byte }

func (d down) Download(context.Context, domain.Message, domain.Media) (io.ReadCloser, int64, error) {
	return io.NopCloser(bytes.NewReader(d.data)), int64(len(d.data)), nil
}

type objects struct{ n int }

func (o *objects) Put(_ context.Context, s domain.SourceRef, m domain.Media, r io.Reader, n int64) (domain.Object, error) {
	o.n++
	io.Copy(io.Discard, r)
	return domain.Object{Source: s, ResourceID: m.ResourceID, Key: "original", Size: n}, nil
}
func TestMissingModalityFailsAfterOriginalPreserved(t *testing.T) {
	o := &objects{}
	p, err := New(Config{Downloader: down{[]byte("image")}, Objects: o})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Process(context.Background(), domain.Message{Source: domain.SourceRef{}, Media: []domain.Media{{Type: domain.MediaImage, MIME: "image/png"}}})
	if !errors.Is(err, ErrModalityUnavailable) || o.n != 1 {
		t.Fatalf("missing modality silently passed: %v objects=%d", err, o.n)
	}
}

type mimeVision struct{}

func (mimeVision) Describe(_ context.Context, mime string, _ []byte) (string, error) {
	return mime, nil
}
func TestImageWithProviderMissingMIMEIsSniffedBeforeModelCall(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	p, _ := New(Config{Downloader: down{b.Bytes()}, Objects: &objects{}, Vision: mimeVision{}})
	a, err := p.Process(context.Background(), domain.Message{Media: []domain.Media{{Type: domain.MediaImage, ResourceID: "image"}}})
	if err != nil || len(a.Parts) != 1 || a.Parts[0].Text != "image/png" {
		t.Fatalf("missing image MIME not normalized %v %v", a, err)
	}
}

type countCloser struct {
	countingReader
	closed bool
}

func (r *countCloser) Close() error { r.closed = true; return nil }

type declaredDown struct{ r *countCloser }

func (d declaredDown) Download(context.Context, domain.Message, domain.Media) (io.ReadCloser, int64, error) {
	return d.r, 1, nil
}
func TestDownloadDeclaredLengthBoundsConsumption(t *testing.T) {
	r := &countCloser{}
	p, _ := New(Config{Downloader: declaredDown{r}, Objects: &objects{}})
	if _, err := p.Process(context.Background(), domain.Message{Media: []domain.Media{{Type: domain.MediaImage}}}); err == nil {
		t.Fatal("length mismatch accepted")
	}
	if r.read > 2 || !r.closed {
		t.Fatalf("declared length consumed %d before rejection", r.read)
	}
}

func officeArchive(entries map[string]string) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for n, s := range entries {
		f, _ := w.Create(n)
		io.WriteString(f, s)
	}
	w.Close()
	return b.Bytes()
}
func TestOfficeHonorsPageLimit(t *testing.T) {
	b := officeArchive(map[string]string{"ppt/slides/slide1.xml": "<a><t>one</t></a>", "ppt/slides/slide2.xml": "<a><t>two</t></a>"})
	l, _ := normalizeLimits(Limits{MaxPages: 1})
	parts, err := extractOffice(b, l)
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("2 slides accepted with MaxPages=1: parts=%d err=%v", len(parts), err)
	}
}
func TestXLSXResolvesSharedStringCells(t *testing.T) {
	b := officeArchive(map[string]string{"xl/sharedStrings.xml": `<sst><si><t>first</t></si><si><t>second</t></si></sst>`, "xl/worksheets/sheet1.xml": `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>1</v></c><c r="B1" t="s"><v>0</v></c></row></sheetData></worksheet>`})
	l, _ := normalizeLimits(Limits{})
	parts, err := extractOffice(b, l)
	if err != nil {
		t.Fatal(err)
	}
	sheet := parts[len(parts)-1].Text
	if !strings.Contains(sheet, "second") || !strings.Contains(sheet, "first") || strings.Contains(sheet, "1") || strings.Contains(sheet, "0") {
		t.Fatalf("shared string references emitted as literal numbers instead of cell values: %q", sheet)
	}
}

func TestXLSXInlineRichTextAndSheetOrder(t *testing.T) {
	b := officeArchive(map[string]string{"xl/sharedStrings.xml": `<sst><si><r><t>first </t></r><r><t>rich</t></r></si></sst>`, "xl/worksheets/sheet10.xml": `<worksheet><sheetData><row><c t="inlineStr"><is><t>inline</t></is></c><c><v>42</v></c></row></sheetData></worksheet>`, "xl/worksheets/sheet2.xml": `<worksheet><sheetData><row><c t="s"><v>0</v></c></row></sheetData></worksheet>`})
	parts, err := extractOffice(b, Limits{})
	if err != nil || len(parts) != 2 || parts[0].Page != 2 || parts[0].Text != "first rich" || parts[1].Page != 10 || !strings.Contains(parts[1].Text, "inline 42") {
		t.Fatalf("sheet identity/order lost %+v %v", parts, err)
	}
}
func TestOfficePageBudgetCheckedBeforeSlideXMLRead(t *testing.T) {
	b := officeArchive(map[string]string{"ppt/slides/slide1.xml": "not even XML", "ppt/slides/slide2.xml": "also malformed"})
	_, err := extractOffice(b, Limits{MaxPages: 1})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("page budget must precede XML parsing: %v", err)
	}
}
