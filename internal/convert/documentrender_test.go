package convert

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentRenderDocx(t *testing.T) {
	t.Run("Letterbox", func(t *testing.T) {
		src := image.NewNRGBA(image.Rect(0, 0, 100, 200))
		red := color.NRGBA{R: 255, A: 255}
		for y := 0; y < 200; y++ {
			for x := 0; x < 100; x++ {
				src.SetNRGBA(x, y, red)
			}
		}
		got := letterboxPage(src, 128)
		if got.Bounds().Dx() != 128 || got.Bounds().Dy() != 128 {
			t.Fatalf("bounds %v", got.Bounds())
		}
		if !pixelWhite(got.NRGBAAt(0, 0)) || !pixelWhite(got.NRGBAAt(127, 0)) || !pixelWhite(got.NRGBAAt(0, 127)) || !pixelWhite(got.NRGBAAt(127, 127)) {
			t.Fatal("corners")
		}
		for y := 0; y < 128; y++ {
			for x := 0; x < 128; x++ {
				px := got.NRGBAAt(x, y)
				inside := x >= 32 && x < 96 && y >= 0 && y < 128
				if inside {
					if px != red {
						t.Fatalf("red %d,%d %+v", x, y, px)
					}
					continue
				}
				if px.R == 255 && px.G == 0 && px.B == 0 {
					t.Fatalf("red outside %d,%d", x, y)
				}
				if !pixelWhite(px) {
					t.Fatalf("margin %d,%d %+v", x, y, px)
				}
			}
		}
	})

	t.Run("Content", func(t *testing.T) {
		requireRenderEngines(t)
		pngBytes := renderDocx(t, docxWithText("X"), nil)
		img := decodePNG(t, pngBytes)
		if img.Bounds().Dx() != 1280 || img.Bounds().Dy() != 1280 {
			t.Fatalf("bounds %v", img.Bounds())
		}
		if !cornersWhite(img) || pageIsBlank(img) {
			t.Fatal("content page")
		}
	})

	t.Run("AllBlank", func(t *testing.T) {
		requireRenderEngines(t)
		pngBytes := renderDocx(t, docxWithText(""), nil)
		img := decodePNG(t, pngBytes)
		if img.Bounds().Dx() != 1280 || !pageIsBlank(img) {
			t.Fatal("blank page")
		}
	})

	t.Run("Workspace", func(t *testing.T) {
		parent := t.TempDir()
		writeMarkPNG(t, filepath.Join(parent, "page-02.png"), 9)
		out := filepath.Join(parent, "out.png")
		err := (DocumentRenderConverter{}).Convert(context.Background(), filepath.Join(parent, "in.docx"), out, nil)
		if err == nil {
			t.Fatal("missing engine")
		}
		entries, err := os.ReadDir(parent)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".doc-render-") {
				t.Fatalf("workspace left %s", entry.Name())
			}
		}
		if _, err := os.Stat(filepath.Join(parent, "page-02.png")); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("png left")
		}
	})

	t.Run("FirstContent", func(t *testing.T) {
		dir := t.TempDir()
		base := filepath.Join(dir, "page")
		writeWhitePNG(t, base+"-01.png")
		writeMarkPNG(t, base+"-02.png", 40)
		if err := os.WriteFile(base+"-03.png", []byte("not a png"), 0o600); err != nil {
			t.Fatal(err)
		}
		img, err := squareFromPageFiles(dir, base, 64)
		if err != nil {
			t.Fatal(err)
		}
		if pageIsBlank(img) || markOf(img) != 40 {
			t.Fatalf("mark %d blank %v", markOf(img), pageIsBlank(img))
		}
	})

	t.Run("TenPages", func(t *testing.T) {
		dir := t.TempDir()
		base := filepath.Join(dir, "page")
		for n := 1; n <= 10; n++ {
			writeMarkPNG(t, fmt.Sprintf("%s-%02d.png", base, n), byte(n))
		}
		if err := os.WriteFile(filepath.Join(dir, "page.png"), []byte("no"), 0o600); err != nil {
			t.Fatal(err)
		}
		pages, err := readRasterPages(dir, base)
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 10 {
			t.Fatalf("pages %d", len(pages))
		}
		for i, page := range pages {
			if markOf(page) != byte(i+1) {
				t.Fatalf("page %d mark %d", i+1, markOf(page))
			}
		}

		mixed := t.TempDir()
		mixedBase := filepath.Join(mixed, "page")
		for _, n := range []int{1, 2, 10} {
			writeMarkPNG(t, fmt.Sprintf("%s-%d.png", mixedBase, n), byte(n))
		}
		pages, err = readRasterPages(mixed, mixedBase)
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 3 || markOf(pages[0]) != 1 || markOf(pages[1]) != 2 || markOf(pages[2]) != 10 {
			t.Fatalf("mixed order %d", len(pages))
		}
	})

	t.Run("LeadingBlank", func(t *testing.T) {
		requireRenderEngines(t)
		pngBytes := renderDocx(t, docxLeadingBlank(), nil)
		img := decodePNG(t, pngBytes)
		if pageIsBlank(img) {
			t.Fatal("leading blank was not skipped")
		}
	})
}

func TestDocumentRenderSize(t *testing.T) {
	if got, err := documentRenderSize(nil); err != nil || got != 1280 {
		t.Fatalf("nil %d %v", got, err)
	}
	if got, err := documentRenderSize(map[string]any{}); err != nil || got != 1280 {
		t.Fatalf("empty %d %v", got, err)
	}
	if got, err := documentRenderSize(map[string]any{"size": 256}); err != nil || got != 256 {
		t.Fatalf("256 %d %v", got, err)
	}
	if got, err := documentRenderSize(map[string]any{"size": 2048}); err != nil || got != 2048 {
		t.Fatalf("2048 %d %v", got, err)
	}
	if got, err := documentRenderSize(map[string]any{"size": float64(1280)}); err != nil || got != 1280 {
		t.Fatalf("json integer %d %v", got, err)
	}
	for _, opts := range []map[string]any{
		{"size": 255},
		{"size": 2049},
		{"size": 256.5},
		{"size": "1280"},
		{"page": 1},
		{"size": 256, "page": 1},
	} {
		dir := t.TempDir()
		out := filepath.Join(dir, "out.png")
		err := (DocumentRenderConverter{}).Convert(context.Background(), filepath.Join(dir, "in.docx"), out, opts)
		if err == nil {
			t.Fatalf("accepted %#v", opts)
		}
		if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
			t.Fatalf("output left for %#v", opts)
		}
	}

	t.Run("Draw", func(t *testing.T) {
		requireRenderEngines(t)
		for _, edge := range []int{256, 1280, 2048} {
			img := decodePNG(t, renderDocx(t, docxWithText("X"), map[string]any{"size": edge}))
			if img.Bounds().Dx() != edge || img.Bounds().Dy() != edge {
				t.Fatalf("edge %d bounds %v", edge, img.Bounds())
			}
		}
		img := decodePNG(t, renderDocx(t, docxWithText("X"), nil))
		if img.Bounds().Dx() != 1280 {
			t.Fatalf("nil draw %v", img.Bounds())
		}
		img = decodePNG(t, renderDocx(t, docxWithText("X"), map[string]any{}))
		if img.Bounds().Dx() != 1280 {
			t.Fatalf("empty draw %v", img.Bounds())
		}
	})
}

func requireRenderEngines(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("soffice is not on PATH")
	}
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm is not on PATH")
	}
}

func renderDocx(t *testing.T, docx []byte, opts map[string]any) []byte {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.docx")
	out := filepath.Join(dir, "out.png")
	if err := os.WriteFile(in, docx, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (DocumentRenderConverter{}).Convert(context.Background(), in, out, opts); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func decodePNG(t *testing.T, body []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func cornersWhite(img image.Image) bool {
	b := img.Bounds()
	points := []image.Point{
		b.Min,
		{X: b.Max.X - 1, Y: b.Min.Y},
		{X: b.Min.X, Y: b.Max.Y - 1},
		{X: b.Max.X - 1, Y: b.Max.Y - 1},
	}
	for _, p := range points {
		r, g, bl, _ := img.At(p.X, p.Y).RGBA()
		if r>>8 < blankChannel || g>>8 < blankChannel || bl>>8 < blankChannel {
			return false
		}
	}
	return true
}

func writeWhitePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	writeTestPNG(t, path, img)
}

func writeMarkPNG(t *testing.T, path string, mark byte) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: mark, A: 255})
	writeTestPNG(t, path, img)
}

func writeTestPNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(f, img)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
}

func markOf(img image.Image) byte {
	r, _, _, _ := img.At(img.Bounds().Min.X, img.Bounds().Min.Y).RGBA()
	return byte(r >> 8)
}

func pixelWhite(c color.NRGBA) bool {
	return c.R >= blankChannel && c.G >= blankChannel && c.B >= blankChannel && c.A == 255
}

func docxWithText(text string) []byte {
	body := `<w:p><w:r><w:rPr><w:sz w:val="144"/><w:szCs w:val="144"/></w:rPr><w:t>` + text + `</w:t></w:r></w:p>`
	if text == "" {
		body = `<w:p/>`
	}
	return zipDocx(body)
}

func docxLeadingBlank() []byte {
	body := `<w:p/><w:p><w:r><w:br w:type="page"/></w:r></w:p><w:p><w:r><w:rPr><w:sz w:val="144"/><w:szCs w:val="144"/></w:rPr><w:t>X</w:t></w:r></w:p>`
	return zipDocx(body)
}

func zipDocx(body string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`</Relationships>`,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
			`<w:body>` + body + `</w:body></w:document>`,
	}
	for name, contents := range files {
		w, err := zw.Create(name)
		if err != nil {
			panic(err)
		}
		if _, err := w.Write([]byte(contents)); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestDocumentRenderSources(t *testing.T) {
	t.Run("Pairs", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join("..", "..", "schema", "manifest-document-render.json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Capabilities []struct {
				ID              string         `json:"id"`
				ParameterSchema map[string]any `json:"parameter_schema"`
				Formats         []Pair         `json:"formats"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Capabilities) != 1 || doc.Capabilities[0].ID != "octoconv.render.document" {
			t.Fatalf("capability %+v", doc.Capabilities)
		}
		if len(doc.Capabilities[0].ParameterSchema) != 0 || bytes.Contains(raw, []byte(`"page"`)) {
			t.Fatal("page field")
		}
		want := []string{"docx", "odt", "xlsx", "ods", "pptx", "odp"}
		pairs := (DocumentRenderConverter{}).Pairs()
		if len(pairs) != len(want) || len(doc.Capabilities[0].Formats) != len(want) {
			t.Fatalf("pairs %d formats %d", len(pairs), len(doc.Capabilities[0].Formats))
		}
		for i, source := range want {
			pair := Pair{From: source, To: "png"}
			if pairs[i] != pair || doc.Capabilities[0].Formats[i] != pair {
				t.Fatalf("format %d got %+v %+v", i, pairs[i], doc.Capabilities[0].Formats[i])
			}
			conv, ok := Default.LookupOp(OperationRender, source, "png")
			if !ok {
				t.Fatalf("missing render %s", source)
			}
			if _, ok := conv.(DocumentRenderConverter); !ok {
				t.Fatalf("converter %T", conv)
			}
			if _, ok := Default.LookupOp(OperationConvert, source, "png"); ok {
				t.Fatalf("convert serves %s png", source)
			}
			if _, err := filterFor(source, "pdf"); err != nil {
				t.Fatal(err)
			}
		}
		for _, source := range []string{"pdf", "html"} {
			if _, ok := Default.LookupOp(OperationRender, source, "png"); ok {
				t.Fatalf("served %s", source)
			}
			dir := t.TempDir()
			out := filepath.Join(dir, "out.png")
			if err := (DocumentRenderConverter{}).Convert(context.Background(), filepath.Join(dir, "in."+source), out, nil); err == nil {
				t.Fatalf("drew %s", source)
			}
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Fatalf("png left for %s", source)
			}
		}
	})

	t.Run("Fixtures", func(t *testing.T) {
		for _, source := range []string{"odt", "xlsx", "ods", "pptx", "odp"} {
			assertZip(t, source, officeWithMark(source))
		}
		assertZip(t, "xlsx", xlsxLeadingBlank())
		assertZip(t, "pptx", pptxLeadingBlank())
	})

	for _, source := range []string{"odt", "xlsx", "ods", "pptx", "odp"} {
		t.Run(source, func(t *testing.T) {
			requireRenderEngines(t)
			img := decodePNG(t, renderOffice(t, source, officeWithMark(source), nil))
			if img.Bounds().Dx() != 1280 || img.Bounds().Dy() != 1280 || !cornersWhite(img) || pageIsBlank(img) {
				t.Fatalf("bounds %v blank %v", img.Bounds(), pageIsBlank(img))
			}
		})
	}

	t.Run("LeadingBlankSheet", func(t *testing.T) {
		requireRenderEngines(t)
		img := decodePNG(t, renderOffice(t, "xlsx", xlsxLeadingBlank(), nil))
		if pageIsBlank(img) {
			t.Fatal("leading blank sheet was not skipped")
		}
	})

	t.Run("LeadingBlankSlide", func(t *testing.T) {
		requireRenderEngines(t)
		img := decodePNG(t, renderOffice(t, "pptx", pptxLeadingBlank(), nil))
		if pageIsBlank(img) {
			t.Fatal("leading blank slide was not skipped")
		}
	})
}

func renderOffice(t *testing.T, format string, body []byte, opts map[string]any) []byte {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in."+format)
	out := filepath.Join(dir, "out.png")
	if err := os.WriteFile(in, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (DocumentRenderConverter{}).Convert(context.Background(), in, out, opts); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func assertZip(t *testing.T, source string, body []byte) {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(source, err)
	}
	if len(r.File) < 3 {
		t.Fatalf("%s files %d", source, len(r.File))
	}
	switch source {
	case "odt", "ods", "odp":
		if r.File[0].Name != "mimetype" || r.File[0].Method != zip.Store {
			t.Fatalf("%s mimetype %+v", source, r.File[0].FileHeader)
		}
	case "xlsx", "pptx":
		if r.File[0].Name != "[Content_Types].xml" {
			t.Fatalf("%s content types %s", source, r.File[0].Name)
		}
	}
}

func officeWithMark(source string) []byte {
	switch source {
	case "odt":
		return odfPackage("application/vnd.oasis.opendocument.text", odtContent())
	case "ods":
		return odfPackage("application/vnd.oasis.opendocument.spreadsheet", odsContent())
	case "odp":
		return odfPackage("application/vnd.oasis.opendocument.presentation", odpContent())
	case "xlsx":
		return xlsxPackage(xlsxSheet("1", "X"), false)
	case "pptx":
		return pptxPackage([]string{pptxShape()})
	default:
		panic(source)
	}
}

func odfPackage(media, content string) []byte {
	manifest := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" manifest:version="1.3">` +
		`<manifest:file-entry manifest:full-path="/" manifest:media-type="` + media + `"/>` +
		`<manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/>` +
		`</manifest:manifest>`
	return zipParts([]zipPart{
		{name: "mimetype", body: media, store: true},
		{name: "content.xml", body: content},
		{name: "META-INF/manifest.xml", body: manifest},
	})
}

func odtContent() string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0" office:version="1.3">` +
		`<office:automatic-styles><style:style style:name="T1" style:family="text"><style:text-properties fo:font-size="72pt" fo:font-weight="bold" fo:color="#000000"/></style:style></office:automatic-styles>` +
		`<office:body><office:text><text:p><text:span text:style-name="T1">X</text:span></text:p></office:text></office:body>` +
		`</office:document-content>`
}

func odsContent() string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0" office:version="1.3">` +
		`<office:automatic-styles><style:style style:name="C1" style:family="table-cell"><style:text-properties fo:font-size="72pt" fo:font-weight="bold" fo:color="#000000"/></style:style></office:automatic-styles>` +
		`<office:body><office:spreadsheet><table:table table:name="Sheet1"><table:table-row><table:table-cell office:value-type="string" table:style-name="C1"><text:p>X</text:p></table:table-cell></table:table-row></table:table></office:spreadsheet></office:body>` +
		`</office:document-content>`
}

func odpContent() string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0" office:version="1.3">` +
		`<office:automatic-styles><style:style style:name="G1" style:family="graphic"><style:graphic-properties draw:fill="solid" draw:fill-color="#000000"/></style:style></office:automatic-styles>` +
		`<office:body><office:presentation><draw:page draw:name="Slide1"><draw:rect draw:style-name="G1" svg:x="4cm" svg:y="4cm" svg:width="10cm" svg:height="8cm"/></draw:page></office:presentation></office:body>` +
		`</office:document-content>`
}

func xlsxLeadingBlank() []byte {
	sheet := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<sheetViews><sheetView showGridLines="0" workbookViewId="0"/></sheetViews>` +
		`<sheetData>` +
		`<row r="1" ht="90" customHeight="1"><c r="A1" s="2" t="inlineStr"><is><t>X</t></is></c></row>` +
		`<row r="2" ht="90" customHeight="1"><c r="A2" s="1" t="inlineStr"><is><t>X</t></is></c></row>` +
		`</sheetData>` +
		`<rowBreaks count="1" manualBreakCount="1"><brk id="1" max="16383" man="1"/></rowBreaks>` +
		`</worksheet>`
	return xlsxPackage(sheet, true)
}

func xlsxSheet(style, text string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<sheetViews><sheetView showGridLines="0" workbookViewId="0"/></sheetViews>` +
		`<sheetData><row r="1" ht="90" customHeight="1"><c r="A1" s="` + style + `" t="inlineStr"><is><t>` + text + `</t></is></c></row></sheetData>` +
		`</worksheet>`
}

func xlsxPackage(sheet string, whiteFont bool) []byte {
	fonts := `<font><sz val="11"/><name val="Calibri"/></font><font><sz val="72"/><color rgb="FF000000"/><name val="Calibri"/><b/></font>`
	xfs := `<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
		`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>`
	fontCount, xfCount := "2", "2"
	if whiteFont {
		fonts += `<font><sz val="72"/><color rgb="FFFFFFFF"/><name val="Calibri"/></font>`
		xfs += `<xf numFmtId="0" fontId="2" fillId="0" borderId="0" xfId="0" applyFont="1"/>`
		fontCount, xfCount = "3", "3"
	}
	styles := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<fonts count="` + fontCount + `">` + fonts + `</fonts>` +
		`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
		`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
		`<cellXfs count="` + xfCount + `">` + xfs + `</cellXfs>` +
		`</styleSheet>`
	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
		`</Types>`
	return zipParts([]zipPart{
		{name: "[Content_Types].xml", body: contentTypes},
		{name: "_rels/.rels", body: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{name: "xl/workbook.xml", body: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{name: "xl/_rels/workbook.xml.rels", body: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`},
		{name: "xl/worksheets/sheet1.xml", body: sheet},
		{name: "xl/styles.xml", body: styles},
	})
}

func pptxLeadingBlank() []byte {
	return pptxPackage([]string{"", pptxShape()})
}

func pptxShape() string {
	return `<p:sp><p:nvSpPr><p:cNvPr id="2" name="Mark"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="914400" y="914400"/><a:ext cx="5486400" cy="3657600"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:solidFill><a:srgbClr val="000000"/></a:solidFill></p:spPr></p:sp>`
}

func pptxPackage(shapes []string) []byte {
	var contentTypes strings.Builder
	contentTypes.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	contentTypes.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	contentTypes.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	contentTypes.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	contentTypes.WriteString(`<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>`)
	var slideIDs, slideRels strings.Builder
	parts := []zipPart{{name: "[Content_Types].xml", body: ""}}
	for i, shape := range shapes {
		n := i + 1
		contentTypes.WriteString(fmt.Sprintf(`<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, n))
		slideIDs.WriteString(fmt.Sprintf(`<p:sldId id="%d" r:id="rId%d"/>`, 255+n, n))
		slideRels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, n, n))
		parts = append(parts, zipPart{
			name: fmt.Sprintf("ppt/slides/slide%d.xml", n),
			body: pptxSlide(shape),
		})
		parts = append(parts, zipPart{
			name: fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", n),
			body: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`,
		})
	}
	contentTypes.WriteString(`</Types>`)
	parts[0].body = contentTypes.String()
	presentation := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">` +
		`<p:sldIdLst>` + slideIDs.String() + `</p:sldIdLst>` +
		`<p:sldSz cx="9144000" cy="6858000" type="screen4x3"/>` +
		`<p:notesSz cx="6858000" cy="9144000"/>` +
		`</p:presentation>`
	parts = append(parts,
		zipPart{name: "_rels/.rels", body: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/></Relationships>`},
		zipPart{name: "ppt/presentation.xml", body: presentation},
		zipPart{name: "ppt/_rels/presentation.xml.rels", body: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + slideRels.String() + `</Relationships>`},
	)
	return zipParts(parts)
}

func pptxSlide(shape string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">` +
		`<p:cSld><p:bg><p:bgPr><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill><a:effectLst/></p:bgPr></p:bg>` +
		`<p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
		`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>` +
		shape +
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
}

type zipPart struct {
	name  string
	body  string
	store bool
}

func zipParts(parts []zipPart) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, part := range parts {
		var (
			w   io.Writer
			err error
		)
		if part.store {
			w, err = zw.CreateHeader(&zip.FileHeader{Name: part.name, Method: zip.Store})
		} else {
			w, err = zw.Create(part.name)
		}
		if err != nil {
			panic(err)
		}
		if _, err := w.Write([]byte(part.body)); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
