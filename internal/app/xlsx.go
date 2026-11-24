package app

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// writeXLSX writes a minimal Office Open XML workbook (.xlsx) with one sheet
// of string cells. No third-party dependency; Excel / Numbers / LibreOffice
// open the result.
func writeXLSX(w io.Writer, sheetName string, rows [][]string) error {
	if sheetName == "" {
		sheetName = "Sheet1"
	}
	// Excel sheet name max 31 chars; strip illegal characters.
	sheetName = sanitizeSheetName(sheetName)

	zw := zip.NewWriter(w)
	files := []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
			`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
			`</Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
			`</Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<sheets><sheet name="` + xmlEscapeAttr(sheetName) + `" sheetId="1" r:id="rId1"/></sheets>` +
			`</workbook>`},
		{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
			`</Relationships>`},
		{"xl/worksheets/sheet1.xml", buildSheetXML(rows)},
	}
	for _, file := range files {
		f, err := zw.Create(file.name)
		if err != nil {
			return err
		}
		if _, err = io.WriteString(f, file.body); err != nil {
			return err
		}
	}
	return zw.Close()
}

func sanitizeSheetName(name string) string {
	replacer := strings.NewReplacer("\\", "", "/", "", "?", "", "*", "", "[", "", "]", "", ":", "")
	name = replacer.Replace(name)
	runes := []rune(name)
	if len(runes) > 31 {
		name = string(runes[:31])
	}
	if name == "" {
		return "Sheet1"
	}
	return name
}

func xmlEscapeAttr(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func buildSheetXML(rows [][]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for i, row := range rows {
		fmt.Fprintf(&b, `<row r="%d">`, i+1)
		for j, cell := range row {
			ref := cellRef(j, i+1)
			b.WriteString(`<c r="`)
			b.WriteString(ref)
			b.WriteString(`" t="inlineStr"><is><t>`)
			_ = xml.EscapeText(&b, []byte(cell))
			b.WriteString(`</t></is></c>`)
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

// cellRef returns A1-style address for zero-based column and 1-based row.
func cellRef(col, row int) string {
	var name []byte
	n := col
	for {
		name = append([]byte{byte('A' + n%26)}, name...)
		n = n/26 - 1
		if n < 0 {
			break
		}
	}
	return string(name) + fmt.Sprintf("%d", row)
}
