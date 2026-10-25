package team

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"gometer/internal/config"
	"gometer/internal/workspace"
)

func testETag(d workspace.Definition) string {
	return fmt.Sprintf(`"test/%s/%d"`, d.ID, d.Revision)
}
func testRevision(w http.ResponseWriter, r *http.Request, id string) (int64, bool) {
	tag := r.Header.Get("If-Match")
	if tag == "" {
		apiFailure(w, 428, "Supply the test ETag in If-Match.")
		return 0, false
	}
	prefix := `"test/` + id + `/`
	if !strings.HasPrefix(tag, prefix) || !strings.HasSuffix(tag, `"`) {
		apiFailure(w, 400, "Invalid test ETag.")
		return 0, false
	}
	text := strings.TrimSuffix(strings.TrimPrefix(tag, prefix), `"`)
	revision, err := strconv.ParseInt(text, 10, 64)
	if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != text {
		apiFailure(w, 400, "Invalid test ETag.")
		return 0, false
	}
	return revision, true
}
func sharedInput(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	return jsonInput(w, r, config.MaxConfigBytes)
}
func jsonInput(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		apiFailure(w, 415, "Use application/json.")
		return nil, false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		apiFailure(w, 400, "Shared test exceeds the input bound.")
		return nil, false
	}
	return data, true
}
func sharedResult(w http.ResponseWriter, status int, d workspace.SharedDefinition) {
	w.Header().Set("ETag", testETag(d.Definition))
	if status == 201 {
		w.Header().Set("Location", "/api/tests/"+d.ID+"/document")
	}
	apiJSON(w, status, d)
}
func (s *Server) sharedTests(w http.ResponseWriter, r *http.Request, actorID string) {
	if r.URL.Path == "/api/tests/validate" && r.Method == "POST" && r.URL.RawQuery == "" {
		raw, ok := sharedInput(w, r)
		if !ok {
			return
		}
		if err := s.db.ValidateSharedDefinition(r.Context(), s.workspaceID, actorID, raw); err != nil {
			repositoryFailure(w, err)
			return
		}
		apiJSON(w, 200, map[string]bool{"valid": true})
		return
	}
	collection := r.URL.Path == "/api/tests"
	if collection && r.Method == "GET" {
		s.tests(w, r, actorID)
		return
	}
	if collection {
		if r.Method != "POST" {
			w.Header().Set("Allow", "GET, POST")
			apiFailure(w, 405, "Use POST to create a shared test.")
			return
		}
		if r.URL.RawQuery != "" {
			apiFailure(w, 400, "Shared test creation does not accept query parameters.")
			return
		}
		raw, ok := sharedInput(w, r)
		if !ok {
			return
		}
		saved, err := s.db.CreateSharedDefinition(r.Context(), s.workspaceID, actorID, raw)
		if err != nil {
			repositoryFailure(w, err)
			return
		}
		sharedResult(w, 201, saved)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/tests/"), "/")
	if len(parts) < 1 || len(parts) > 2 || !workspace.ValidID(parts[0]) || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if r.Method == "GET" {
			s.tests(w, r, actorID)
			return
		}
		w.Header().Set("Allow", "GET")
		apiFailure(w, 405, "Use the shared document route to edit a test.")
		return
	}
	switch parts[1] {
	case "document", "export":
		if r.Method == "GET" {
			saved, err := s.db.SharedDefinition(r.Context(), s.workspaceID, actorID, id)
			if err != nil {
				repositoryFailure(w, err)
				return
			}
			if parts[1] == "export" {
				w.Header().Set("Content-Disposition", `attachment; filename="shared-test.json"`)
				w.Header().Set("ETag", testETag(saved.Definition))
				apiJSON(w, 200, saved.Document)
			} else {
				sharedResult(w, 200, saved)
			}
			return
		}
		if parts[1] != "document" || r.Method != "PUT" {
			w.Header().Set("Allow", "GET")
			apiFailure(w, 405, "Method is not supported.")
			return
		}
		revision, ok := testRevision(w, r, id)
		if !ok {
			return
		}
		raw, ok := sharedInput(w, r)
		if !ok {
			return
		}
		saved, err := s.db.UpdateSharedDefinition(r.Context(), s.workspaceID, actorID, id, revision, raw)
		if err != nil {
			repositoryFailure(w, err)
			return
		}
		sharedResult(w, 200, saved)
	case "copy":
		if r.Method != "POST" {
			w.Header().Set("Allow", "POST")
			apiFailure(w, 405, "Use POST to copy a shared test.")
			return
		}
		revision, ok := testRevision(w, r, id)
		if !ok {
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
		if err != nil || len(body) != 0 {
			apiFailure(w, 400, "Copy does not accept a request body.")
			return
		}
		saved, err := s.db.CopySharedDefinition(r.Context(), s.workspaceID, actorID, id, revision)
		if err != nil {
			repositoryFailure(w, err)
			return
		}
		sharedResult(w, 201, saved)
	default:
		http.NotFound(w, r)
	}
}
