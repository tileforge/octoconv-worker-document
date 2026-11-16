package convert

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPairsMatchTheManifestAndFilters(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schema", "manifest-document-convert.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Capabilities []struct {
			ID     string `json:"id"`
			Legacy struct {
				Operation string `json:"operation"`
				Engine    string `json:"engine"`
			} `json:"legacy"`
			Formats []Pair `json:"formats"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Capabilities) != 1 {
		t.Fatalf("capabilities %d", len(doc.Capabilities))
	}
	gotCap := doc.Capabilities[0]
	if gotCap.ID != "octoconv.convert.document" || gotCap.Legacy.Engine != EngineDocument || gotCap.Legacy.Operation != OperationConvert {
		t.Fatalf("capability %+v", gotCap)
	}
	pairs := (LibreOfficeConverter{}).Pairs()
	if len(pairs) != 12 || len(gotCap.Formats) != 12 || len(filterTable) != 12 {
		t.Fatalf("pairs %d formats %d filters %d", len(pairs), len(gotCap.Formats), len(filterTable))
	}
	for i, pair := range pairs {
		if gotCap.Formats[i] != pair {
			t.Fatalf("format %d %+v want %+v", i, gotCap.Formats[i], pair)
		}
		if _, err := filterFor(pair.From, pair.To); err != nil {
			t.Fatal(err)
		}
		conv, ok := Default.LookupOp(OperationConvert, pair.From, pair.To)
		if !ok {
			t.Fatalf("missing %s to %s", pair.From, pair.To)
		}
		if bad := conv.Convert(context.Background(), "in."+pair.From, filepath.Join(t.TempDir(), "out."+pair.To), map[string]any{"pdf_profile": "pdfa-2b"}); bad == nil {
			t.Fatal("option accepted")
		}
	}
	if _, err := filterFor("docx", "ods"); err == nil {
		t.Fatal("cross-family filter")
	}
}

func TestValidateOutput(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "out.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateOutput(pdf, "pdf"); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain.pdf")
	if err := os.WriteFile(plain, []byte("not a pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateOutput(plain, "pdf"); err == nil {
		t.Fatal("plain pdf accepted")
	}
	empty := filepath.Join(dir, "empty.pdf")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateOutput(empty, "pdf"); err == nil {
		t.Fatal("empty accepted")
	}

	for _, format := range []string{"docx", "xlsx", "pptx", "odt", "ods", "odp"} {
		path := filepath.Join(dir, "out."+format)
		if err := os.WriteFile(path, containerBytes(t, format), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := validateOutput(path, format); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
	}
	wrong := filepath.Join(dir, "wrong.odt")
	if err := os.WriteFile(wrong, containerBytes(t, "docx"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateOutput(wrong, "odt"); err == nil {
		t.Fatal("docx accepted as odt")
	}
}

func TestSniffRejectsADuplicateRoot(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for range 2 {
		w, err := zw.Create("word/document.xml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("<w:document/>")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := SniffContainer(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err == nil {
		t.Fatal("duplicate root accepted")
	}
}

func containerBytes(t *testing.T, format string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	switch format {
	case "docx", "xlsx", "pptx":
		name := map[string]string{
			"docx": "word/document.xml",
			"xlsx": "xl/workbook.xml",
			"pptx": "ppt/presentation.xml",
		}[format]
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("<root/>")); err != nil {
			t.Fatal(err)
		}
	default:
		header := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		media := map[string]string{
			"odt": "application/vnd.oasis.opendocument.text",
			"ods": "application/vnd.oasis.opendocument.spreadsheet",
			"odp": "application/vnd.oasis.opendocument.presentation",
		}[format]
		if _, err := w.Write([]byte(media)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
