package dashboard

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// TestSource records definition provenance, independently of runs or JMX source.
type TestSource struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
}

// Pre-revision records remain readable without rewriting the user's workspace.
func definitionRevision(saved SavedScenario) uint64 {
	if saved.Revision == 0 {
		return 1
	}
	return saved.Revision
}
func definitionETag(saved SavedScenario) string {
	return fmt.Sprintf(`"%d"`, definitionRevision(saved))
}

func checkDefinitionRevision(w http.ResponseWriter, saved SavedScenario, expected string) bool {
	if expected == "" {
		fail(w, 428, "Reload the saved test before updating it; an If-Match revision is required.")
		return false
	}
	if expected != definitionETag(saved) {
		jsonResponse(w, 409, map[string]any{"error": "This test changed after you opened it. Keep your draft and reload the latest revision, or save your work as a new test.", "revision": definitionRevision(saved)})
		return false
	}
	return true
}

func (s *Server) copyDefinition(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != "POST" {
		fail(w, 405, "Method not allowed.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16))
	if err != nil || strings.TrimSpace(string(body)) != "" {
		fail(w, 400, "Copy uses the saved definition; send no configuration or identity fields.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.store.scenarios[id]
	if !ok {
		fail(w, 404, "Test not found.")
		return
	}
	if !checkDefinitionRevision(w, source, r.Header.Get("If-Match")) {
		return
	}
	if len(s.store.scenarios) >= maxScenarios {
		fail(w, 409, "Test library is full (200).")
		return
	}
	newID, err := newID()
	if err != nil {
		fail(w, 500, "Could not create a test ID.")
		return
	}
	// Copy values are serialized immediately. Config is never mutated in place.
	now := time.Now().UTC()
	saved := SavedScenario{ID: newID, Name: source.Name, Config: source.Config, Migration: source.Migration, UpdatedAt: now, CreatedAt: &now, Revision: 1, Source: &TestSource{ID: id, Revision: definitionRevision(source)}}
	if err := s.store.write("scenarios", newID, saved); err != nil {
		fail(w, 500, "Could not persist the test copy.")
		return
	}
	s.store.scenarios[newID] = saved
	w.Header().Set("ETag", definitionETag(saved))
	jsonResponse(w, 201, saved)
}

// Definition previews are deliberately projections, never raw configurations.
type definitionPreview struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Revision  uint64      `json:"revision"`
	UpdatedAt time.Time   `json:"updated_at"`
	CreatedAt *time.Time  `json:"created_at,omitempty"`
	Source    *TestSource `json:"source,omitempty"`
	Preview   struct {
		TargetOrigin  string `json:"target_origin"`
		Mode          string `json:"mode"`
		ScenarioCount int    `json:"scenario_count"`
		WorkerCount   int    `json:"worker_count"`
	} `json:"preview"`
}

func previewDefinition(saved SavedScenario) definitionPreview {
	p := definitionPreview{ID: saved.ID, Name: saved.Name, Revision: definitionRevision(saved), UpdatedAt: saved.UpdatedAt, CreatedAt: saved.CreatedAt, Source: saved.Source}
	if u, err := url.Parse(saved.Config.URL); err == nil {
		p.Preview.TargetOrigin = (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
	}
	p.Preview.Mode = saved.Config.Mode
	p.Preview.ScenarioCount = len(saved.Config.Scenarios)
	p.Preview.WorkerCount = len(saved.Config.Workers)
	return p
}
func (s *Server) definitionsAPI(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/tests" && !strings.HasPrefix(r.URL.Path, "/api/tests/") {
		return false
	}
	if r.Method != "GET" {
		fail(w, 405, "Method not allowed.")
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Path == "/api/tests" {
		list := make([]definitionPreview, 0, len(s.store.scenarios))
		for _, saved := range s.store.scenarios {
			list = append(list, previewDefinition(saved))
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].UpdatedAt.Equal(list[j].UpdatedAt) {
				return list[i].ID < list[j].ID
			}
			return list[i].UpdatedAt.After(list[j].UpdatedAt)
		})
		jsonResponse(w, 200, list)
		return true
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/tests/")
	saved, ok := s.store.scenarios[id]
	if !validID(id) || !ok {
		fail(w, 404, "Test not found.")
		return true
	}
	jsonResponse(w, 200, previewDefinition(saved))
	return true
}
