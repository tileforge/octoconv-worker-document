package convert

import (
	"archive/zip"
	"fmt"
	"io"
)

var ooxmlRootParts = map[string]string{
	"word/document.xml":    "docx",
	"xl/workbook.xml":      "xlsx",
	"ppt/presentation.xml": "pptx",
}

var odfMimetypes = map[string]string{
	"application/vnd.oasis.opendocument.text":         "odt",
	"application/vnd.oasis.opendocument.spreadsheet":  "ods",
	"application/vnd.oasis.opendocument.presentation": "odp",
}

// SniffContainer reads a ZIP central directory and returns docx, xlsx, pptx, odt, ods, or odp.
// Uncompressed sizes in that directory are chosen by the file author, so a caller must not
// treat a successful sniff as a bound on the bytes a later extract would write.
func SniffContainer(r io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("not a zip: %w", err)
	}
	var format string
	seen := map[string]int{}
	for i, f := range zr.File {
		if name, ok := ooxmlRootParts[f.Name]; ok {
			seen[f.Name]++
			format = name
		}
		if i != 0 || f.Name != "mimetype" || f.Method != zip.Store {
			continue
		}
		payload, err := readStored(f, 128)
		if err != nil {
			return "", fmt.Errorf("mimetype entry: %w", err)
		}
		if odf, ok := odfMimetypes[string(payload)]; ok {
			format = odf
		}
	}
	for _, count := range seen {
		if count > 1 {
			return "", fmt.Errorf("duplicate root part")
		}
	}
	if format == "" {
		return "", fmt.Errorf("unrecognized container")
	}
	return format, nil
}

func readStored(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, max))
}
