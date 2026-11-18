package convert

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
)

var modernExtensions = map[string]struct{}{".pdf": {}, ".docx": {}, ".pptx": {}, ".xlsx": {}}
var legacyTargets = map[string]string{".doc": "docx", ".ppt": "pptx", ".xls": "xlsx"}

type Converter struct {
	libreOffice string
	timeout     time.Duration
}

func New(libreOffice string, timeout time.Duration) *Converter {
	if libreOffice == "" {
		libreOffice = "libreoffice"
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &Converter{libreOffice: libreOffice, timeout: timeout}
}

func (c *Converter) Supports(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	_, modern := modernExtensions[extension]
	_, legacy := legacyTargets[extension]
	return modern || legacy
}

func (c *Converter) Convert(ctx context.Context, path string) (string, error) {
	extension := strings.ToLower(filepath.Ext(path))
	if target, legacy := legacyTargets[extension]; legacy {
		return c.convertLegacy(ctx, path, target)
	}
	var markdown string
	var err error
	switch extension {
	case ".pdf":
		markdown, err = pdfToMarkdown(path)
	case ".docx":
		markdown, err = docxToMarkdown(path)
	case ".pptx":
		markdown, err = pptxToMarkdown(path)
	case ".xlsx":
		markdown, err = xlsxToMarkdown(path)
	default:
		return "", fmt.Errorf("unsupported conversion format %q", extension)
	}
	if err != nil {
		return "", err
	}
	markdown = normalize(markdown)
	if markdown == "" {
		return "", errors.New("conversion produced no text")
	}
	destination := strings.TrimSuffix(path, filepath.Ext(path)) + ".md"
	if err := writeAtomic(destination, []byte(markdown+"\n")); err != nil {
		return "", err
	}
	return destination, nil
}

func pdfToMarkdown(path string) (string, error) {
	file, reader, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening PDF: %w", err)
	}
	defer file.Close()
	text, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("extracting PDF text: %w", err)
	}
	raw, err := io.ReadAll(text)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func docxToMarkdown(path string) (string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("opening DOCX: %w", err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			return extractOOXML(file)
		}
	}
	return "", errors.New("DOCX is missing word/document.xml")
}

func pptxToMarkdown(path string) (string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("opening PPTX: %w", err)
	}
	defer archive.Close()
	var slides []*zip.File
	for _, file := range archive.File {
		if strings.HasPrefix(file.Name, "ppt/slides/slide") && strings.HasSuffix(file.Name, ".xml") {
			slides = append(slides, file)
		}
	}
	sort.Slice(slides, func(i, j int) bool { return slideNumber(slides[i].Name) < slideNumber(slides[j].Name) })
	if len(slides) == 0 {
		return "", errors.New("PPTX contains no slides")
	}
	var output strings.Builder
	for index, slide := range slides {
		text, err := extractOOXML(slide)
		if err != nil {
			return "", fmt.Errorf("reading slide %d: %w", index+1, err)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		fmt.Fprintf(&output, "## Slide %d\n\n%s\n\n", index+1, text)
	}
	return output.String(), nil
}

func extractOOXML(file *zip.File) (string, error) {
	stream, err := file.Open()
	if err != nil {
		return "", err
	}
	defer stream.Close()
	decoder := xml.NewDecoder(io.LimitReader(stream, 64<<20))
	var output strings.Builder
	var textDepth int
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "t" {
				textDepth++
			}
		case xml.CharData:
			if textDepth > 0 {
				output.Write(value)
				output.WriteByte(' ')
			}
		case xml.EndElement:
			if value.Name.Local == "t" {
				textDepth--
			}
			if value.Name.Local == "p" || value.Name.Local == "tr" {
				output.WriteByte('\n')
			}
		}
	}
	return output.String(), nil
}

func xlsxToMarkdown(path string) (string, error) {
	workbook, err := excelize.OpenFile(path)
	if err != nil {
		return "", fmt.Errorf("opening XLSX: %w", err)
	}
	defer workbook.Close()
	var output strings.Builder
	for _, sheet := range workbook.GetSheetList() {
		rows, err := workbook.GetRows(sheet)
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&output, "## %s\n\n", sheet)
		width := 0
		for _, row := range rows {
			if len(row) > width {
				width = len(row)
			}
		}
		writeTableRow(&output, rows[0], width)
		output.WriteByte('|')
		for range width {
			output.WriteString(" --- |")
		}
		output.WriteByte('\n')
		for _, row := range rows[1:] {
			writeTableRow(&output, row, width)
		}
		output.WriteByte('\n')
	}
	return output.String(), nil
}

func writeTableRow(output *strings.Builder, row []string, width int) {
	output.WriteByte('|')
	for index := 0; index < width; index++ {
		value := ""
		if index < len(row) {
			value = row[index]
		}
		value = strings.ReplaceAll(value, "|", "\\|")
		value = strings.ReplaceAll(value, "\n", "<br>")
		fmt.Fprintf(output, " %s |", value)
	}
	output.WriteByte('\n')
}

func (c *Converter) convertLegacy(ctx context.Context, path, target string) (string, error) {
	temporary, err := os.MkdirTemp("", "nous-office-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temporary)
	conversionCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	profile := filepath.Join(temporary, "profile")
	command := exec.CommandContext(conversionCtx, c.libreOffice, "--headless", "-env:UserInstallation=file://"+profile, "--convert-to", target, "--outdir", temporary, path)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("converting legacy Office file: %w: %s", err, strings.TrimSpace(string(output)))
	}
	converted := filepath.Join(temporary, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+"."+target)
	markdownPath, err := c.Convert(ctx, converted)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(markdownPath)
	if err != nil {
		return "", err
	}
	destination := strings.TrimSuffix(path, filepath.Ext(path)) + ".md"
	if err := writeAtomic(destination, raw); err != nil {
		return "", err
	}
	return destination, nil
}

func slideNumber(name string) int {
	base := strings.TrimSuffix(filepath.Base(name), ".xml")
	number, _ := strconv.Atoi(strings.TrimPrefix(base, "slide"))
	return number
}
func normalize(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	lines := strings.Split(value, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
func writeAtomic(path string, content []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".markdown-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o640); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
