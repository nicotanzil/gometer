package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gometer/examples"
	"gometer/internal/jmx"
)

const maxMigrationDrafts = 50

// MigrationSource preserves the source and explicit conversion decisions. A
// draft has no engine configuration and cannot be submitted as a runnable test.
type MigrationSource struct {
	Source   string `json:"source"`
	Filename string `json:"filename"`
	jmx.Options
}
type MigrationDraft struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
	MigrationSource
}
type MigrationSummary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Filename  string    `json:"filename"`
	UpdatedAt time.Time `json:"updated_at"`
}
type migrationRequest struct {
	MigrationSource
	Name string `json:"name,omitempty"`
}

func readMigration(w http.ResponseWriter, r *http.Request) (migrationRequest, jmx.Report, error) {
	var req migrationRequest
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return req, jmx.Report{}, fmt.Errorf("send the JMX import as application/json")
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 6*jmx.MaxSourceBytes+65536))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		return req, jmx.Report{}, fmt.Errorf("invalid JMX import request: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return req, jmx.Report{}, fmt.Errorf("send exactly one JMX import object")
	}
	if len(req.Name) > 256 || len(req.Filename) > 256 {
		return req, jmx.Report{}, fmt.Errorf("import names must not exceed 256 bytes")
	}
	req.Filename = filepath.Base(req.Filename)
	if req.Filename == "." {
		req.Filename = "imported.jmx"
	}
	report, err := jmx.Analyze(strings.NewReader(req.Source), req.Options)
	return req, report, err
}

func (s *store) migrationList() []MigrationSummary {
	list := make([]MigrationSummary, 0, len(s.migrations))
	for _, draft := range s.migrations {
		list = append(list, MigrationSummary{draft.ID, draft.Name, draft.Filename, draft.UpdatedAt})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
	return list
}
func (s *Server) jmxAPI(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/jmx/") {
		return false
	}
	path := r.URL.Path
	if r.Method == "GET" && path == "/api/jmx/example" {
		data, err := examples.Files.ReadFile("jmx/http-smoke.jmx")
		if err != nil {
			fail(w, 500, "JMX sample unavailable.")
			return true
		}
		jsonResponse(w, 200, MigrationSource{Source: string(data), Filename: "http-smoke.jmx"})
		return true
	}
	if r.Method == "POST" && (path == "/api/jmx/analyze" || path == "/api/jmx/convert" || path == "/api/jmx/migrations") {
		s.saveMigration(w, r, "")
		return true
	}
	if strings.HasPrefix(path, "/api/jmx/migrations/") {
		id := strings.TrimPrefix(path, "/api/jmx/migrations/")
		if !validID(id) {
			fail(w, 404, "Migration draft not found.")
			return true
		}
		if r.Method == "PUT" {
			s.saveMigration(w, r, id)
			return true
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		draft, ok := s.store.migrations[id]
		if !ok {
			fail(w, 404, "Migration draft not found.")
			return true
		}
		switch r.Method {
		case "GET":
			jsonResponse(w, 200, draft)
		case "DELETE":
			if err := os.Remove(filepath.Join(s.store.dir, "migrations", id+".json")); err != nil {
				fail(w, 500, "Could not delete migration draft.")
				return true
			}
			delete(s.store.migrations, id)
			jsonResponse(w, 200, map[string]bool{"deleted": true})
		default:
			fail(w, 405, "Method not allowed.")
		}
		return true
	}
	fail(w, 404, "JMX operation not found.")
	return true
}
func (s *Server) saveMigration(w http.ResponseWriter, r *http.Request, id string) {
	req, report, err := readMigration(w, r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if r.URL.Path == "/api/jmx/analyze" {
		jsonResponse(w, 200, report)
		return
	}
	if req.Name == "" {
		req.Name = report.Name
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Path == "/api/jmx/convert" {
		// Re-analyze the original XML here; client-supplied reports/configurations
		// cannot suppress blockers or forge runtime acknowledgement.
		if !report.Ready || report.Config == nil {
			fail(w, 409, "Resolve all blockers and required inputs, then accept the runtime policies before saving a scenario.")
			return
		}
		if len(s.store.scenarios) >= maxScenarios {
			fail(w, 409, "Scenario library is full.")
			return
		}
		newID, err := newID()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		c := *report.Config
		c.Name = req.Name
		saved := SavedScenario{ID: newID, Name: c.Name, UpdatedAt: time.Now().UTC(), Config: c, Migration: &req.MigrationSource}
		if err := s.store.write("scenarios", newID, saved); err != nil {
			fail(w, 500, "Could not save imported scenario.")
			return
		}
		s.store.scenarios[newID] = saved
		jsonResponse(w, 201, saved)
		return
	}
	if id != "" {
		if _, ok := s.store.migrations[id]; !ok {
			fail(w, 404, "Migration draft not found.")
			return
		}
	} else {
		if len(s.store.migrations) >= maxMigrationDrafts {
			fail(w, 409, "Migration draft library is full (50 drafts).")
			return
		}
		id, err = newID()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
	}
	draft := MigrationDraft{ID: id, Name: req.Name, UpdatedAt: time.Now().UTC(), MigrationSource: req.MigrationSource}
	if err := s.store.write("migrations", id, draft); err != nil {
		fail(w, 500, "Could not save migration draft.")
		return
	}
	s.store.migrations[id] = draft
	jsonResponse(w, 200, draft)
}
