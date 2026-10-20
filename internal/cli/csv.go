package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gometer/internal/config"
)

type csvFlags struct{ path, scenario, sharing, eof, delimiter, columns *string }

func addCSVFlags(fs *flag.FlagSet) csvFlags {
	return csvFlags{
		fs.String("csv", "", "CSV file to embed in a users-mode journey"),
		fs.String("csv-scenario", "", "journey name for CSV; required when multiple journeys exist"),
		fs.String("csv-sharing", "shared", "CSV rows: shared or per_user"),
		fs.String("csv-eof", "recycle", "CSV EOF: recycle or stop_user"),
		fs.String("csv-delimiter", ",", "single CSV delimiter character; first row must contain headers"),
		fs.String("csv-columns", "", `JSON mapping of CSV headers to variables; empty values ignore columns`),
	}
}
func (f csvFlags) apply(fs *flag.FlagSet, c *config.Config) error {
	provided := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name != "csv" && len(flag.Name) > 4 && flag.Name[:4] == "csv-" {
			provided = true
		}
	})
	if *f.path == "" {
		if provided {
			return fmt.Errorf("CSV options require -csv")
		}
		return nil
	}
	selected := -1
	for i, s := range c.Scenarios {
		if s.Name == *f.scenario || (*f.scenario == "" && len(c.Scenarios) == 1) {
			selected = i
		}
	}
	if selected < 0 {
		return fmt.Errorf("CSV requires an existing journey; choose it with -csv-scenario when multiple journeys exist")
	}
	file, err := os.Open(*f.path)
	if err != nil {
		return fmt.Errorf("read CSV: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, config.MaxConfigBytes+1))
	if err != nil {
		return fmt.Errorf("read CSV: %w", err)
	}
	if len(content) > config.MaxConfigBytes {
		return fmt.Errorf("CSV exceeds 1 MiB")
	}
	dataset := &config.CSV{Name: filepath.Base(*f.path), Content: string(content), Sharing: *f.sharing, OnEOF: *f.eof, Delimiter: *f.delimiter}
	if *f.columns != "" {
		if err := json.Unmarshal([]byte(*f.columns), &dataset.Columns); err != nil {
			return fmt.Errorf("CSV columns must be a JSON object of string values")
		}
	}
	if _, err := config.CompileCSV(*dataset); err != nil {
		return err
	}
	c.Scenarios[selected].CSV = dataset
	c.Scenarios[selected].Data = nil
	return nil
}
