package team

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"gometer/internal/config"
	"gometer/internal/sharedtest"
	"gometer/internal/workspace"
)

func draftETag(d workspace.Draft) string { return fmt.Sprintf(`"draft/%s/%d"`, d.ID, d.Revision) }
func draftRevision(w http.ResponseWriter, r *http.Request, id string) (int64, bool) {
	tag := r.Header.Get("If-Match")
	if tag == "" {
		apiFailure(w, 428, "Supply the draft ETag in If-Match.")
		return 0, false
	}
	prefix := `"draft/` + id + `/`
	if !strings.HasPrefix(tag, prefix) || !strings.HasSuffix(tag, `"`) {
		apiFailure(w, 400, "Invalid draft ETag.")
		return 0, false
	}
	text := strings.TrimSuffix(strings.TrimPrefix(tag, prefix), `"`)
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n < 1 || strconv.FormatInt(n, 10) != text {
		apiFailure(w, 400, "Invalid draft ETag.")
		return 0, false
	}
	return n, true
}
func (s *Server) drafts(w http.ResponseWriter, r *http.Request, actorID string) {
	collection := r.URL.Path == "/api/drafts"
	id := strings.TrimPrefix(r.URL.Path, "/api/drafts/")
	if r.URL.RawQuery != "" || !collection && !workspace.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	if r.Method == "GET" {
		if collection {
			list, err := s.db.ListDrafts(r.Context(), s.workspaceID, actorID)
			if err != nil {
				repositoryFailure(w, err)
				return
			}
			apiJSON(w, 200, map[string]any{"drafts": list})
			return
		}
		draft, err := s.db.Draft(r.Context(), s.workspaceID, actorID, id)
		if err != nil {
			repositoryFailure(w, err)
			return
		}
		w.Header().Set("ETag", draftETag(draft))
		apiJSON(w, 200, draft)
		return
	}
	if collection && r.Method != "POST" || !collection && r.Method != "PUT" && r.Method != "DELETE" {
		apiFailure(w, 405, "Use POST on the draft collection or PUT/DELETE on a draft.")
		return
	}
	var revision int64
	if !collection {
		var ok bool
		revision, ok = draftRevision(w, r, id)
		if !ok {
			return
		}
	}
	if r.Method == "DELETE" {
		if err := s.db.DeleteDraft(r.Context(), s.workspaceID, actorID, id, revision); err != nil {
			repositoryFailure(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	raw, ok := jsonInput(w, r, config.MaxConfigBytes+1024)
	if !ok {
		return
	}
	var input struct {
		Base     *workspace.DefinitionSource `json:"base,omitempty"`
		Document json.RawMessage             `json:"document"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if !sharedtest.UnambiguousJSON(raw) || dec.Decode(&input) != nil {
		apiFailure(w, 400, "Supply a draft document and optional base test revision.")
		return
	}
	// Require canonical envelope keys as well as the unambiguous JSON and typed
	// decoding above; the nested document has its own strict parser.
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || len(envelope) > 2 {
		apiFailure(w, 400, "Invalid draft envelope.")
		return
	}
	// Require canonical envelope keys. Document content has its own strict parser.
	for key := range envelope {
		if key != "base" && key != "document" {
			apiFailure(w, 400, "Invalid draft envelope.")
			return
		}
	}
	if collection {
		id = ""
	}
	draft, err := s.db.SaveDraft(r.Context(), s.workspaceID, actorID, id, revision, input.Base, input.Document)
	if err != nil {
		repositoryFailure(w, err)
		return
	}
	w.Header().Set("ETag", draftETag(draft))
	status := 200
	if collection {
		status = 201
		w.Header().Set("Location", "/api/drafts/"+draft.ID)
	}
	apiJSON(w, status, draft)
}
