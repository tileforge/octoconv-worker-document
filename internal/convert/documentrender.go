package convert

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func init() {
	Default.Register(DocumentRenderConverter{})
}

const (
	documentRenderDefault = 1280
	documentRenderMin     = 256
	documentRenderMax     = 2048
	blankChannel          = 250
)

// DocumentRenderConverter draws one office file as a primary square PNG.
// A zero value runs soffice and pdftoppm directly. It does not apply Landlock.
type DocumentRenderConverter struct{}

// Operation reports the render operation.
func (DocumentRenderConverter) Operation() string { return OperationRender }

// Pairs returns the docx to png pair.
func (DocumentRenderConverter) Pairs() []Pair {
	return []Pair{{From: "docx", To: "png"}}
}

// Convert writes one square PNG at outPath.
// A nil or empty opts map uses edge 1280. The only accepted key is size.
func (DocumentRenderConverter) Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error {
	if err := renderDocument(ctx, inPath, outPath, opts); err != nil {
		_ = os.Remove(outPath)
		return err
	}
	return nil
}

func renderDocument(ctx context.Context, inPath, outPath string, opts map[string]any) error {
	edge, err := documentRenderSize(opts)
	if err != nil {
		return err
	}
	workDir := filepath.Dir(outPath)
	pdfPath, err := exportDocumentPDF(ctx, inPath, workDir)
	if err != nil {
		return err
	}
	pages, err := rasterPDF(ctx, pdfPath, workDir, edge)
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		return fmt.Errorf("document render: pdftoppm wrote no page")
	}
	var chosen image.Image
	for _, page := range pages {
		if pageIsBlank(page) {
			continue
		}
		chosen = page
		break
	}
	var img image.Image
	if chosen == nil {
		img = whiteSquare(edge)
	} else {
		img = letterboxPage(chosen, edge)
	}
	return writePNG(outPath, img)
}

func documentRenderSize(opts map[string]any) (int, error) {
	if len(opts) == 0 {
		return documentRenderDefault, nil
	}
	if len(opts) != 1 {
		return 0, fmt.Errorf("document render: unsupported option")
	}
	raw, ok := opts["size"]
	if !ok {
		return 0, fmt.Errorf("document render: unsupported option")
	}
	n, err := renderSizeInt(raw)
	if err != nil {
		return 0, err
	}
	if n < documentRenderMin || n > documentRenderMax {
		return 0, fmt.Errorf("document render: size %d out of range", n)
	}
	return n, nil
}

func renderSizeInt(raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case float64:
		if v != math.Trunc(v) || math.IsInf(v, 0) || math.IsNaN(v) {
			return 0, fmt.Errorf("document render: size is not an integer")
		}
		return int(v), nil
	default:
		return 0, fmt.Errorf("document render: size is not an integer")
	}
}

func exportDocumentPDF(ctx context.Context, inPath, workDir string) (string, error) {
	profileDir := filepath.Join(workDir, "lo-profile")
	tmpDir := filepath.Join(workDir, "tmp")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return "", fmt.Errorf("document render: mkdir profile: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return "", fmt.Errorf("document render: mkdir engine tmp: %w", err)
	}
	filter, err := filterFor(filepath.Ext(inPath), "pdf")
	if err != nil {
		return "", fmt.Errorf("document render: %w", err)
	}
	args := []string{
		"--headless", "--invisible", "--nocrashreport", "--nodefault",
		"--nologo", "--nofirststartwizard", "--norestore",
		"-env:UserInstallation=file://" + profileDir,
		"--convert-to", "pdf:" + filter,
		"--outdir", workDir,
		inPath,
	}
	if err := runCommand(ctx, engineEnv(tmpDir), "soffice", args...); err != nil {
		return "", fmt.Errorf("document render: %w", err)
	}
	produced := filepath.Join(workDir, strings.TrimSuffix(filepath.Base(inPath), filepath.Ext(inPath))+".pdf")
	if err := validateOutput(produced, "pdf"); err != nil {
		return "", err
	}
	return produced, nil
}

// pdftoppm names each page base-1.png, base-2.png, and so on. -scale-to sets the longer side.
func rasterPDF(ctx context.Context, pdfPath, workDir string, edge int) ([]image.Image, error) {
	tmpDir := filepath.Join(workDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return nil, fmt.Errorf("document render: mkdir engine tmp: %w", err)
	}
	base := filepath.Join(workDir, "page")
	err := runCommand(ctx, engineEnv(tmpDir), "pdftoppm", "-png", "-scale-to", strconv.Itoa(edge), pdfPath, base)
	if err != nil {
		return nil, fmt.Errorf("document render: %w", err)
	}
	var pages []image.Image
	for n := 1; ; n++ {
		path := fmt.Sprintf("%s-%d.png", base, n)
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return nil, fmt.Errorf("document render: open page: %w", err)
		}
		img, decErr := png.Decode(f)
		closeErr := f.Close()
		if decErr != nil {
			return nil, fmt.Errorf("document render: decode page: %w", decErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("document render: close page: %w", closeErr)
		}
		pages = append(pages, img)
	}
	return pages, nil
}

func pageIsBlank(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 < blankChannel || g>>8 < blankChannel || bl>>8 < blankChannel {
				return false
			}
		}
	}
	return true
}

func letterboxPage(src image.Image, edge int) *image.NRGBA {
	dst := whiteSquare(edge)
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw <= 0 || sh <= 0 || edge <= 0 {
		return dst
	}
	longer := sw
	if sh > longer {
		longer = sh
	}
	dw := sw * edge / longer
	dh := sh * edge / longer
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	ox := (edge - dw) / 2
	oy := (edge - dh) / 2
	for y := 0; y < dh; y++ {
		sy := sb.Min.Y + y*sh/dh
		for x := 0; x < dw; x++ {
			sx := sb.Min.X + x*sw/dw
			dst.SetNRGBA(ox+x, oy+y, color.NRGBAModel.Convert(src.At(sx, sy)).(color.NRGBA))
		}
	}
	return dst
}

func whiteSquare(edge int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, edge, edge))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
		img.Pix[i+1] = 255
		img.Pix[i+2] = 255
		img.Pix[i+3] = 255
	}
	return img
}

func writePNG(path string, img image.Image) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("document render: create png: %w", err)
	}
	encErr := png.Encode(f, img)
	closeErr := f.Close()
	if encErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("document render: encode png: %w", encErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("document render: close png: %w", closeErr)
	}
	return nil
}
