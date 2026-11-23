package convert

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
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
