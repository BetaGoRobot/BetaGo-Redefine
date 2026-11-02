package media

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

var officePageName = regexp.MustCompile(`^(?:ppt/slides/slide|xl/worksheets/sheet)([0-9]+)\.xml$`)

type officePage struct {
	file  *zip.File
	page  int
	sheet bool
}

func officeEntry(f *zip.File, max int64) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("office entry unavailable")
	}
	defer r.Close()
	return boundedRead(r, max)
}

func extractOffice(b []byte, l Limits) ([]domain.MediaPart, error) {
	var err error
	l, err = normalizeLimits(l)
	if err != nil {
		return nil, err
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("invalid office archive")
	}
	if len(z.File) > l.MaxZipEntries {
		return nil, ErrLimit
	}
	var total uint64
	var shared *zip.File
	pages := []officePage{}
	seen := map[string]bool{}
	// Directory budgets precede opening/decompressing any XML member. Shared
	// strings are an XLSX lookup table, never an independent output page.
	for _, f := range z.File {
		if f.UncompressedSize64 > uint64(l.MaxZipBytes) || total > uint64(l.MaxZipBytes)-f.UncompressedSize64 {
			return nil, ErrLimit
		}
		total += f.UncompressedSize64
		n := path.Clean(f.Name)
		if seen[n] {
			return nil, fmt.Errorf("duplicate office archive member")
		}
		seen[n] = true
		if n == "xl/sharedStrings.xml" {
			shared = f
			continue
		}
		if n == "word/document.xml" {
			pages = append(pages, officePage{file: f, page: 1})
			continue
		}
		match := officePageName.FindStringSubmatch(n)
		if len(match) == 2 {
			number, e := strconv.Atoi(match[1])
			if e != nil || number < 1 {
				return nil, fmt.Errorf("invalid office page identity")
			}
			pages = append(pages, officePage{file: f, page: number, sheet: strings.HasPrefix(n, "xl/")})
		}
	}
	if len(pages) > l.MaxPages {
		return nil, ErrLimit
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("office archive contains no supported pages")
	}
	sort.Slice(pages, func(i, j int) bool {
		if pages[i].sheet != pages[j].sheet {
			return !pages[i].sheet
		}
		return pages[i].page < pages[j].page
	})
	var sharedStrings []string
	if shared != nil {
		raw, e := officeEntry(shared, l.MaxZipBytes)
		if e != nil {
			return nil, e
		}
		sharedStrings, e = parseSharedStrings(raw, l.MaxTextBytes)
		if e != nil {
			return nil, e
		}
	}
	parts := []domain.MediaPart{}
	used := int64(0)
	for _, page := range pages {
		raw, e := officeEntry(page.file, l.MaxZipBytes)
		if e != nil {
			return nil, e
		}
		var text string
		if page.sheet {
			text, e = parseSheet(raw, sharedStrings, l.MaxTextBytes-used)
		} else {
			text, e = parseOfficeText(raw, l.MaxTextBytes-used)
		}
		if e != nil {
			return nil, e
		}
		used += int64(len(text))
		if used > l.MaxTextBytes {
			return nil, ErrLimit
		}
		parts = append(parts, domain.MediaPart{Text: text, Page: page.page})
	}
	return parts, nil
}

type xmlBudget struct {
	decoder       *xml.Decoder
	depth, tokens int
}

func newXMLBudget(b []byte) *xmlBudget {
	return &xmlBudget{decoder: xml.NewDecoder(bytes.NewReader(b))}
}
func (d *xmlBudget) next() (xml.Token, error) {
	token, err := d.decoder.Token()
	if err != nil {
		return token, err
	}
	d.tokens++
	if d.tokens > 1_000_000 {
		return nil, ErrLimit
	}
	switch token.(type) {
	case xml.StartElement:
		d.depth++
	case xml.EndElement:
		d.depth--
	}
	if d.depth > 128 {
		return nil, ErrLimit
	}
	return token, nil
}
func appendOfficeText(b *strings.Builder, s string, max int64) error {
	if int64(b.Len())+int64(len(s)) > max {
		return ErrLimit
	}
	b.WriteString(s)
	return nil
}
func parseSharedStrings(raw []byte, max int64) ([]string, error) {
	dec := newXMLBudget(raw)
	out := []string{}
	var item strings.Builder
	capture := false
	used := int64(0)
	for {
		token, err := dec.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid office shared strings: %w", safeXMLError(err))
		}
		switch v := token.(type) {
		case xml.StartElement:
			if v.Name.Local == "si" {
				item.Reset()
			}
			if v.Name.Local == "t" {
				capture = true
			}
		case xml.EndElement:
			if v.Name.Local == "t" {
				capture = false
			}
			if v.Name.Local == "si" {
				if len(out) >= 65536 {
					return nil, ErrLimit
				}
				s := item.String()
				used += int64(len(s))
				if used > max {
					return nil, ErrLimit
				}
				out = append(out, s)
			}
		case xml.CharData:
			if capture {
				if err = appendOfficeText(&item, string(v), max-used); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}
func safeXMLError(err error) error {
	if err == ErrLimit {
		return ErrLimit
	}
	return fmt.Errorf("malformed XML")
}
func parseSheet(raw []byte, shared []string, max int64) (string, error) {
	dec := newXMLBudget(raw)
	var text, value strings.Builder
	typ := ""
	capture := false
	inCell := false
	for {
		token, err := dec.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", safeXMLError(err)
		}
		switch v := token.(type) {
		case xml.StartElement:
			switch v.Name.Local {
			case "c":
				inCell = true
				value.Reset()
				typ = ""
				for _, a := range v.Attr {
					if a.Name.Local == "t" {
						typ = a.Value
					}
				}
			case "v", "t":
				capture = inCell
			}
		case xml.CharData:
			if capture {
				if err = appendOfficeText(&value, string(v), max-int64(text.Len())); err != nil {
					return "", err
				}
			}
		case xml.EndElement:
			switch v.Name.Local {
			case "v", "t":
				capture = false
			case "c":
				cell := value.String()
				if typ == "s" {
					index, e := strconv.Atoi(strings.TrimSpace(cell))
					if e != nil || index < 0 || index >= len(shared) {
						return "", fmt.Errorf("invalid spreadsheet shared string reference")
					}
					cell = shared[index]
				}
				if typ == "b" {
					switch strings.TrimSpace(cell) {
					case "1":
						cell = "true"
					case "0":
						cell = "false"
					}
				}
				if cell != "" {
					if text.Len() > 0 {
						if err = appendOfficeText(&text, " ", max); err != nil {
							return "", err
						}
					}
					if err = appendOfficeText(&text, cell, max); err != nil {
						return "", err
					}
				}
				inCell = false
			case "row":
				if text.Len() > 0 {
					if err = appendOfficeText(&text, "\n", max); err != nil {
						return "", err
					}
				}
			}
		}
	}
	return strings.TrimSpace(text.String()), nil
}
func parseOfficeText(raw []byte, max int64) (string, error) {
	dec := newXMLBudget(raw)
	var text strings.Builder
	capture := false
	for {
		token, err := dec.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", safeXMLError(err)
		}
		switch v := token.(type) {
		case xml.StartElement:
			if v.Name.Local == "t" {
				capture = true
			}
		case xml.EndElement:
			if v.Name.Local == "t" {
				capture = false
				if err = appendOfficeText(&text, " ", max); err != nil {
					return "", err
				}
			}
			if v.Name.Local == "p" {
				if err = appendOfficeText(&text, "\n", max); err != nil {
					return "", err
				}
			}
		case xml.CharData:
			if capture {
				if err = appendOfficeText(&text, string(v), max); err != nil {
					return "", err
				}
			}
		}
	}
	return strings.TrimSpace(text.String()), nil
}
