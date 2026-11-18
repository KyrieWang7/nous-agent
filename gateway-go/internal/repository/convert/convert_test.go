package convert

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestConvertModernDocuments(t *testing.T) {
	root := t.TempDir()
	docx := filepath.Join(root, "brief.docx")
	writeZip(t, docx, map[string]string{"word/document.xml": `<w:document xmlns:w="w"><w:body><w:p><w:r><w:t>Hello DOCX</w:t></w:r></w:p><w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p></w:body></w:document>`})
	pptx := filepath.Join(root, "deck.pptx")
	writeZip(t, pptx, map[string]string{"ppt/slides/slide2.xml": `<p:sld xmlns:p="p" xmlns:a="a"><a:p><a:r><a:t>Second slide</a:t></a:r></a:p></p:sld>`, "ppt/slides/slide1.xml": `<p:sld xmlns:p="p" xmlns:a="a"><a:p><a:r><a:t>First slide</a:t></a:r></a:p></p:sld>`})
	xlsx := filepath.Join(root, "table.xlsx")
	workbook := excelize.NewFile()
	_ = workbook.SetCellValue("Sheet1", "A1", "Name")
	_ = workbook.SetCellValue("Sheet1", "B1", "Value")
	_ = workbook.SetCellValue("Sheet1", "A2", "alpha")
	_ = workbook.SetCellValue("Sheet1", "B2", 42)
	if err := workbook.SaveAs(xlsx); err != nil {
		t.Fatal(err)
	}
	_ = workbook.Close()
	pdfPath := filepath.Join(root, "note.pdf")
	writeMinimalPDF(t, pdfPath, "Hello PDF")

	converter := New("", time.Second)
	tests := []struct{ path, want string }{{docx, "Hello DOCX"}, {pptx, "## Slide 1"}, {xlsx, "| Name | Value |"}, {pdfPath, "Hello PDF"}}
	for _, test := range tests {
		t.Run(filepath.Ext(test.path), func(t *testing.T) {
			output, err := converter.Convert(context.Background(), test.path)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), test.want) {
				t.Fatalf("markdown %q does not contain %q", raw, test.want)
			}
		})
	}
}

func TestConvertLegacyOfficeUsesNormalizedOOXML(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy.doc")
	if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "fixture.docx")
	writeZip(t, fixture, map[string]string{"word/document.xml": `<w:document xmlns:w="w"><w:body><w:p><w:r><w:t>Legacy content</w:t></w:r></w:p></w:body></w:document>`})
	script := filepath.Join(root, "libreoffice")
	body := "#!/bin/sh\nout=''\nlast=''\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = '--outdir' ]; then shift; out=\"$1\"; fi\n  last=\"$1\"\n  shift\ndone\ncp \"$FIXTURE_DOCX\" \"$out/legacy.docx\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIXTURE_DOCX", fixture)
	output, err := New(script, 5*time.Second).Convert(context.Background(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Legacy content") {
		t.Fatalf("markdown=%q", raw)
	}
}

func TestConvertRealLegacyOffice(t *testing.T) {
	binary := os.Getenv("LIBREOFFICE_INTEGRATION_PATH")
	if binary == "" {
		t.Skip("LIBREOFFICE_INTEGRATION_PATH is not set")
	}
	root := t.TempDir()
	textPath := filepath.Join(root, "legacy.txt")
	if err := os.WriteFile(textPath, []byte("Real legacy Word content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "--headless", "-env:UserInstallation=file://"+filepath.Join(root, "source-profile"), "--convert-to", "doc:MS Word 97", "--outdir", root, textPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("creating DOC fixture: %v: %s", err, output)
	}
	csvPath := filepath.Join(root, "legacy-sheet.csv")
	if err := os.WriteFile(csvPath, []byte("Name,Value\nalpha,42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command = exec.Command(binary, "--headless", "-env:UserInstallation=file://"+filepath.Join(root, "sheet-profile"), "--convert-to", "xls:MS Excel 97", "--outdir", root, csvPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("creating XLS fixture: %v: %s", err, output)
	}
	fodpPath := filepath.Join(root, "legacy-slide.fodp")
	fodp := `<?xml version="1.0" encoding="UTF-8"?><office:document xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:presentation="urn:oasis:names:tc:opendocument:xmlns:presentation:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" office:mimetype="application/vnd.oasis.opendocument.presentation" office:version="1.3"><office:body><office:presentation><draw:page draw:name="Slide 1"><draw:frame presentation:class="title"><draw:text-box><text:p>Real legacy slide content</text:p></draw:text-box></draw:frame></draw:page></office:presentation></office:body></office:document>`
	if err := os.WriteFile(fodpPath, []byte(fodp), 0o600); err != nil {
		t.Fatal(err)
	}
	command = exec.Command(binary, "--headless", "-env:UserInstallation=file://"+filepath.Join(root, "slide-profile"), "--convert-to", "ppt:MS PowerPoint 97", "--outdir", root, fodpPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("creating PPT fixture: %v: %s", err, output)
	}
	converter := New(binary, 30*time.Second)
	tests := []struct{ path, want string }{{filepath.Join(root, "legacy.doc"), "Real legacy Word content"}, {filepath.Join(root, "legacy-sheet.xls"), "alpha"}, {filepath.Join(root, "legacy-slide.ppt"), "Real legacy slide content"}}
	for _, test := range tests {
		output, err := converter.Convert(context.Background(), test.path)
		if err != nil {
			t.Fatalf("converting %s: %v", test.path, err)
		}
		raw, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), test.want) {
			t.Fatalf("markdown %q does not contain %q", raw, test.want)
		}
	}
}

func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, value := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeMinimalPDF(t *testing.T, path, text string) {
	t.Helper()
	stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>", fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream), "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for index, object := range objects {
		offsets = append(offsets, output.Len())
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&output, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	if err := os.WriteFile(path, output.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
