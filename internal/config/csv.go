package config

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"gometer/internal/correlation"
)

const MaxCSVRows = 10000
const MaxCSVColumns = 100
const MaxCSVCells = 100000

// CSV keeps source text in the configuration. Only explicit CLI file flags read
// files; a saved or imported scenario never resolves a filesystem path.
type CSV struct {
	Name      string            `json:"name,omitempty"`
	Content   string            `json:"content"`
	Delimiter string            `json:"delimiter,omitempty"`
	Columns   map[string]string `json:"columns,omitempty"`
	Sharing   string            `json:"sharing,omitempty"`
	OnEOF     string            `json:"on_eof,omitempty"`
}

func (c CSV) SharingMode() string {
	if c.Sharing == "" {
		return "shared"
	}
	return c.Sharing
}
func (c CSV) EOFAction() string {
	if c.OnEOF == "" {
		return "recycle"
	}
	return c.OnEOF
}
func (c CSV) Label() string {
	if c.Name == "" {
		return "CSV dataset"
	}
	return c.Name
}

type CSVPreview struct {
	Headers  []string          `json:"headers"`
	Sample   [][]string        `json:"sample"`
	RowCount int               `json:"row_count"`
	Columns  map[string]string `json:"columns"`
}

type CSVPlan struct {
	CSV
	Rows []map[string]string
}

func readCSV(content, delimiter string) ([]string, [][]string, error) {
	if len(content) > MaxConfigBytes || !utf8.ValidString(content) {
		return nil, nil, fmt.Errorf("CSV must be UTF-8 and at most 1 MiB")
	}
	if delimiter == "" {
		delimiter = ","
	}
	r, n := utf8.DecodeRuneInString(delimiter)
	if n != len(delimiter) || r == utf8.RuneError || r == 0 || r == '"' || r == '\r' || r == '\n' {
		return nil, nil, fmt.Errorf("CSV delimiter must be one character other than a quote or newline")
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(content, "\ufeff")))
	reader.Comma = r
	headers, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("CSV needs a header row")
	}
	if len(headers) > MaxCSVColumns {
		return nil, nil, fmt.Errorf("CSV supports at most %d columns", MaxCSVColumns)
	}
	seen := map[string]bool{}
	for _, h := range headers {
		if h == "" || len(h) > 256 || seen[h] {
			return nil, nil, fmt.Errorf("CSV column names must be nonempty, unique and at most 256 bytes")
		}
		seen[h] = true
	}
	var rows [][]string
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("CSV record %d is invalid or has a different column count", len(rows)+2)
		}
		if len(rows) >= MaxCSVRows || (len(rows)+1)*len(headers) > MaxCSVCells {
			return nil, nil, fmt.Errorf("CSV exceeds %d data rows or %d cells", MaxCSVRows, MaxCSVCells)
		}
		for _, v := range row {
			if len(v) > correlation.MaxValueBytes {
				return nil, nil, fmt.Errorf("CSV cell exceeds %d bytes", correlation.MaxValueBytes)
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("CSV needs at least one data row")
	}
	return headers, rows, nil
}

// PreviewCSV parses with the same strict dialect as execution. Suggested names
// are presented for review; native configurations never silently rename columns.
func PreviewCSV(content, delimiter string) (CSVPreview, error) {
	headers, rows, err := readCSV(content, delimiter)
	if err != nil {
		return CSVPreview{}, err
	}
	mapping := map[string]string{}
	used := map[string]bool{}
	for _, h := range headers {
		base := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
				return r
			}
			return '_'
		}, h)
		if !variableAllowed(base) {
			base = "data_" + base
		}
		if len(base) > 100 {
			base = base[:100]
		}
		name := base
		for i := 2; used[name]; i++ {
			name = fmt.Sprintf("%s_%d", base, i)
		}
		used[name] = true
		mapping[h] = name
	}
	return CSVPreview{Headers: headers, Sample: rows[:min(5, len(rows))], RowCount: len(rows), Columns: mapping}, nil
}

func CompileCSV(c CSV) (*CSVPlan, error) {
	if len(c.Name) > 256 {
		return nil, fmt.Errorf("CSV name must be at most 256 bytes")
	}
	if c.SharingMode() != "shared" && c.SharingMode() != "per_user" {
		return nil, fmt.Errorf("CSV sharing must be shared or per_user")
	}
	if c.EOFAction() != "recycle" && c.EOFAction() != "stop_user" {
		return nil, fmt.Errorf("CSV on_eof must be recycle or stop_user")
	}
	headers, rows, err := readCSV(c.Content, c.Delimiter)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	targets := make([]string, len(headers))
	for i, h := range headers {
		known[h] = true
		target := h
		if value, ok := c.Columns[h]; ok {
			target = value
		}
		if target != "" && !variableAllowed(target) {
			return nil, fmt.Errorf("CSV column %q needs a valid, non-reserved variable name", h)
		}
		targets[i] = target
	}
	for h := range c.Columns {
		if !known[h] {
			return nil, fmt.Errorf("CSV mapping refers to unknown column %q", h)
		}
	}
	used := map[string]bool{}
	for _, target := range targets {
		if target == "" {
			continue
		}
		if used[target] {
			return nil, fmt.Errorf("CSV columns must map to distinct variable names")
		}
		used[target] = true
	}
	if len(used) == 0 {
		return nil, fmt.Errorf("Map at least one CSV column to a variable")
	}
	p := &CSVPlan{CSV: c, Rows: make([]map[string]string, len(rows))}
	for i, row := range rows {
		values := map[string]string{}
		for j, v := range row {
			if targets[j] != "" {
				values[targets[j]] = v
			}
		}
		p.Rows[i] = values
	}
	return p, nil
}

func (p Plan) HasCSV() bool {
	for _, s := range p.ScenarioPlans {
		if s.CSV != nil {
			return true
		}
	}
	return false
}

func (p Plan) ScenarioOrdinal(id int64, selected int) int64 {
	s := p.ScenarioPlans[selected]
	return (id/p.TotalWeight)*s.Weight + min(max(id%p.TotalWeight-s.WeightStart, int64(0)), s.Weight)
}
