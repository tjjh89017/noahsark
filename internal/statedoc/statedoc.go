// Package statedoc reads the markdown tables of docs/states.md, so that a
// test can check the code against the document row by row.
package statedoc

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Table is one markdown table: the cells of its header line and the cells
// of each row. Each cell is trimmed. A `\|` in a cell is a literal bar.
type Table struct {
	Header []string
	Rows   [][]string
}

// Column returns the index of the header cell name, or -1.
func (t *Table) Column(name string) int {
	for i, h := range t.Header {
		if h == name {
			return i
		}
	}
	return -1
}

// SplitLine returns the cells of a markdown table line. A `\|` is a
// literal bar.
func SplitLine(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cell.WriteByte('|')
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

// isDelimiter reports whether cells are the delimiter line under a
// header, such as `|---|:--:|`.
func isDelimiter(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, ":-") != "" || !strings.Contains(c, "-") {
			return false
		}
	}
	return true
}

// Tables returns each table of the markdown file path, in file order. A
// table is a header line, a delimiter line, and the next lines that start
// with a bar.
func Tables(path string) ([]Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var tables []Table
	inTable := false
	var pending []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "|") {
			inTable, pending = false, nil
			continue
		}
		cells := SplitLine(line)
		switch {
		case inTable:
			last := &tables[len(tables)-1]
			last.Rows = append(last.Rows, cells)
		case pending != nil && isDelimiter(cells):
			tables = append(tables, Table{Header: pending})
			inTable, pending = true, nil
		default:
			pending = cells
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return tables, nil
}

// Find returns the first table of path whose header holds each of the
// columns.
func Find(path string, columns ...string) (*Table, error) {
	tables, err := Tables(path)
	if err != nil {
		return nil, err
	}
next:
	for i := range tables {
		for _, c := range columns {
			if tables[i].Column(c) < 0 {
				continue next
			}
		}
		return &tables[i], nil
	}
	return nil, fmt.Errorf("%s holds no table with the columns %s", path, strings.Join(columns, ", "))
}
