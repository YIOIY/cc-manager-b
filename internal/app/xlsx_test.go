package app

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestWriteXLSXRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeXLSX(&buf, "渠道用量", [][]string{
		{"日期", "账号 ID", "消耗额度（美元）"},
		{"2026-11-20", "438802", "18.78"},
		{"合计", "", "18.78"},
	}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		byName[f.Name] = string(body)
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/worksheets/sheet1.xml"} {
		if byName[name] == "" {
			t.Fatalf("missing %s", name)
		}
	}
	sheet := byName["xl/worksheets/sheet1.xml"]
	for _, want := range []string{"日期", "438802", "18.78", "渠道用量"} {
		if want == "渠道用量" {
			if !strings.Contains(byName["xl/workbook.xml"], want) {
				t.Fatalf("workbook missing sheet name: %s", byName["xl/workbook.xml"])
			}
			continue
		}
		if !strings.Contains(sheet, want) {
			t.Fatalf("sheet missing %q: %s", want, sheet)
		}
	}
	if cellRef(0, 1) != "A1" || cellRef(25, 2) != "Z2" || cellRef(26, 3) != "AA3" {
		t.Fatalf("cellRef A1=%s Z2=%s AA3=%s", cellRef(0, 1), cellRef(25, 2), cellRef(26, 3))
	}
}

func TestSanitizeSheetName(t *testing.T) {
	if got := sanitizeSheetName(`a/b\c?*[:x]`); strings.ContainsAny(got, `/\?*[]:`) {
		t.Fatalf("illegal chars remain: %q", got)
	}
	long := strings.Repeat("测", 40)
	if got := sanitizeSheetName(long); len([]rune(got)) != 31 {
		t.Fatalf("len=%d", len([]rune(got)))
	}
}
