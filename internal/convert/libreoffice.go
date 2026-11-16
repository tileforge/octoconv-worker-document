package convert

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	Default.Register(LibreOfficeConverter{})
}

// LibreOfficeConverter shells out to soffice for the twelve document pairs.
// A zero value runs soffice directly. It does not apply Landlock.
type LibreOfficeConverter struct{}

// Pairs returns the twelve pairs in LibreOffice filter order.
func (LibreOfficeConverter) Pairs() []Pair {
	sources := []string{"docx", "odt", "xlsx", "ods", "pptx", "odp"}
	pairs := make([]Pair, 0, 12)
	for _, source := range sources {
		pairs = append(pairs, Pair{From: source, To: "pdf"})
	}
	pairs = append(pairs,
		Pair{From: "docx", To: "odt"},
		Pair{From: "odt", To: "docx"},
		Pair{From: "xlsx", To: "ods"},
		Pair{From: "ods", To: "xlsx"},
		Pair{From: "pptx", To: "odp"},
		Pair{From: "odp", To: "pptx"},
	)
	return pairs
}

var filterTable = map[[2]string]string{
	{"docx", "pdf"}: "writer_pdf_Export",
	{"odt", "pdf"}:  "writer_pdf_Export",
	{"xlsx", "pdf"}: "calc_pdf_Export",
	{"ods", "pdf"}:  "calc_pdf_Export",
	{"pptx", "pdf"}: "impress_pdf_Export",
	{"odp", "pdf"}:  "impress_pdf_Export",
	{"docx", "odt"}: "writer8",
	{"odt", "docx"}: "MS Word 2007 XML",
	{"xlsx", "ods"}: "calc8",
	{"ods", "xlsx"}: "Calc MS Excel 2007 XML",
	{"pptx", "odp"}: "impress8",
	{"odp", "pptx"}: "Impress MS PowerPoint 2007 XML",
}

func filterFor(sourceExt, targetFormat string) (string, error) {
	key := [2]string{NormalizeFormat(sourceExt), NormalizeFormat(targetFormat)}
	filter, ok := filterTable[key]
	if !ok {
		return "", fmt.Errorf("no export filter for %s -> %s", sourceExt, targetFormat)
	}
	return filter, nil
}

// Convert runs soffice --headless for the pair implied by the two paths.
// opts must be empty. pdf_profile is not on a protocol assignment, and a
// non-empty map would otherwise be ignored.
func (LibreOfficeConverter) Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error {
	if len(opts) != 0 {
		return fmt.Errorf("libreoffice: unsupported option")
	}
	workDir := filepath.Dir(outPath)
	profileDir := filepath.Join(workDir, "lo-profile")
	tmpDir := filepath.Join(workDir, "tmp")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return fmt.Errorf("libreoffice: mkdir profile: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return fmt.Errorf("libreoffice: mkdir engine tmp: %w", err)
	}
	target := NormalizeFormat(filepath.Ext(outPath))
	filter, err := filterFor(filepath.Ext(inPath), target)
	if err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}
	args := []string{
		"--headless", "--invisible", "--nocrashreport", "--nodefault",
		"--nologo", "--nofirststartwizard", "--norestore",
		"-env:UserInstallation=file://" + profileDir,
		"--convert-to", target + ":" + filter,
		"--outdir", workDir,
		inPath,
	}
	if err := runCommand(ctx, engineEnv(tmpDir), "soffice", args...); err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}
	produced := filepath.Join(workDir, strings.TrimSuffix(filepath.Base(inPath), filepath.Ext(inPath))+"."+target)
	if _, err := os.Stat(produced); err != nil {
		return fmt.Errorf("libreoffice: produced no output file for %q: %v", target, err)
	}
	if err := os.Rename(produced, outPath); err != nil {
		return fmt.Errorf("libreoffice: rename output: %w", err)
	}
	if err := validateOutput(outPath, target); err != nil {
		return err
	}
	return nil
}

var pdfMagic = []byte("%PDF-")

func validateOutput(path, target string) error {
	info, err := statOutput(path)
	if err != nil {
		return fmt.Errorf("libreoffice: stat output: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("libreoffice: output is empty")
	}
	if target == "pdf" {
		if info.Size() < int64(len(pdfMagic)) {
			return fmt.Errorf("libreoffice: output missing %%PDF- magic bytes")
		}
		buf, err := readHeader(path, len(pdfMagic))
		if err != nil {
			return fmt.Errorf("libreoffice: read output header: %w", err)
		}
		if !bytes.Equal(buf, pdfMagic) {
			return fmt.Errorf("libreoffice: output missing %%PDF- magic bytes")
		}
		return nil
	}
	f, _, err := OpenJobFile(path)
	if err != nil {
		return fmt.Errorf("libreoffice: open output: %w", err)
	}
	defer f.Close()
	got, err := SniffContainer(f, info.Size())
	if err != nil || got != target {
		return fmt.Errorf("libreoffice: output does not match expected container format %s", target)
	}
	return nil
}
