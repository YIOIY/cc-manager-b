package app

import (
	"cc-manager/internal/errs"
	"net/http"
	"strconv"
	"strings"
)

const maxPageNumber = 1000000

var allowedPageSizes = map[int]bool{10: true, 20: true, 50: true, 100: true}

// pageParams reads page/page_size. paged is false when the client sent
// neither, which keeps the legacy array response for older callers.
func pageParams(w http.ResponseWriter, r *http.Request) (page, size int, paged, ok bool) {
	q := r.URL.Query()
	if q.Get("page") == "" && q.Get("page_size") == "" {
		return 1, 0, false, true
	}
	page, size = 1, 20
	var err error
	var problems errs.Fields
	if v := q.Get("page"); v != "" {
		if page, err = strconv.Atoi(v); err != nil || page < 1 || page > maxPageNumber {
			problems.Add("page", errs.FieldOutOfRange, "页码无效")
		}
	}
	if v := q.Get("page_size"); v != "" {
		if size, err = strconv.Atoi(v); err != nil || !allowedPageSizes[size] {
			problems.Add("page_size", errs.FieldUnsupportedValue, "每页数量应为 10、20、50 或 100")
		}
	}
	if problems.Len() > 0 {
		writeError(w, r, errs.New(errs.CodeQueryInvalid).WithFields(problems.Items()...))
		return 0, 0, true, false
	}
	return page, size, true, true
}

// listFilter accumulates WHERE fragments for simple list endpoints.
type listFilter struct {
	where []string
	args  []any
}

func (f *listFilter) equals(column, value string) {
	if value = strings.TrimSpace(value); value != "" {
		f.where = append(f.where, column+"=?")
		f.args = append(f.args, value)
	}
}

func (f *listFilter) contains(column, value string) {
	if value = strings.TrimSpace(value); value != "" {
		f.where = append(f.where, "instr(lower("+column+"),lower(?))>0")
		f.args = append(f.args, value)
	}
}

// dateRange filters a RFC3339/ISO column by calendar day (inclusive).
func (f *listFilter) dateRange(column, from, to string) {
	if from = strings.TrimSpace(from); from != "" {
		f.where = append(f.where, "substr("+column+",1,10)>=?")
		f.args = append(f.args, from)
	}
	if to = strings.TrimSpace(to); to != "" {
		f.where = append(f.where, "substr("+column+",1,10)<=?")
		f.args = append(f.args, to)
	}
}

// keyword matches every whitespace-separated word against any of the columns.
func (f *listFilter) keyword(value string, columns ...string) {
	for _, word := range strings.Fields(strings.ToLower(value)) {
		parts := make([]string, len(columns))
		for i, column := range columns {
			parts[i] = "instr(lower(" + column + "),?)>0"
			f.args = append(f.args, word)
		}
		f.where = append(f.where, "("+strings.Join(parts, " OR ")+")")
	}
}

func (f *listFilter) sql() string {
	if len(f.where) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(f.where, " AND ")
}

func pagedEnvelope(items []any, page, size, total int) map[string]any {
	return map[string]any{"items": items, "pagination": map[string]int{"page": page, "page_size": size, "total": total}}
}
