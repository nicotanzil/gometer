package team

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gometer/internal/sharedtest"
	"gometer/internal/workspace"
)

// api exposes explicitly shared documents and metadata only. Personal raw
// configuration and execution endpoints are never mounted on this service.
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	tests := path == "/api/tests" || strings.HasPrefix(path, "/api/tests/")
	secrets := path == "/api/secret-references" || strings.HasPrefix(path, "/api/secret-references/")
	drafts := path == "/api/drafts" || strings.HasPrefix(path, "/api/drafts/")
	if !tests && !secrets && !drafts {
		http.NotFound(w, r)
		return
	}
	token := cookieValue(r, sessionCookie)
	session, err := s.db.ResolveSession(r.Context(), s.workspaceID, token)
	if err != nil {
		authError(w, err)
		return
	}
	if r.Method != "GET" && r.Method != "POST" && r.Method != "PUT" && !(drafts && r.Method == "DELETE") {
		w.Header().Set("Allow", "GET, POST, PUT")
		apiFailure(w, http.StatusMethodNotAllowed, "Method is not supported.")
		return
	}
	if r.Method != "GET" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gometer-CSRF")), []byte(csrfToken(token))) != 1 {
		apiFailure(w, http.StatusForbidden, "Request could not be verified.")
		return
	}
	if tests {
		s.sharedTests(w, r, session.Member.ID)
	} else if drafts {
		s.drafts(w, r, session.Member.ID)
	} else {
		s.secrets(w, r, session.Member.ID)
	}
}
func (s *Server) tests(w http.ResponseWriter, r *http.Request, actorID string) {
	if r.URL.Path != "/api/tests" {
		id := strings.TrimPrefix(r.URL.Path, "/api/tests/")
		if !workspace.ValidID(id) || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		definition, err := s.db.DefinitionMetadata(r.Context(), s.workspaceID, actorID, id)
		if err != nil {
			repositoryFailure(w, err)
			return
		}
		w.Header().Set("ETag", fmt.Sprintf(`"test/%s/%d"`, id, definition.Revision))
		apiJSON(w, http.StatusOK, definition)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 2048 || len(query["scope"]) > 1 || len(query["search"]) > 1 || len(query.Get("search")) > 256 {
		apiFailure(w, 400, "Invalid Tests filter.")
		return
	}
	for name := range query {
		if name != "scope" && name != "search" {
			apiFailure(w, 400, "Invalid Tests filter.")
			return
		}
	}
	scope := query.Get("scope")
	if scope != "" && scope != "all" && scope != "mine" {
		apiFailure(w, 400, "Use scope=all or scope=mine.")
		return
	}
	definitions, err := s.db.ListDefinitions(r.Context(), s.workspaceID, actorID, query.Get("search"), scope == "mine")
	if err != nil {
		repositoryFailure(w, err)
		return
	}
	apiJSON(w, 200, struct {
		Tests []workspace.Definition `json:"tests"`
	}{definitions})
}

type secretInput struct {
	Name    string `json:"name"`
	Key     string `json:"source_key"`
	Enabled *bool  `json:"enabled,omitempty"`
}

func secretETag(ref workspace.SecretReference) string {
	return fmt.Sprintf(`"secret/%s/%d"`, ref.ID, ref.Revision)
}
func (s *Server) secrets(w http.ResponseWriter, r *http.Request, actorID string) {
	collection := r.URL.Path == "/api/secret-references"
	id := strings.TrimPrefix(r.URL.Path, "/api/secret-references/")
	if !collection && !workspace.ValidID(id) || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	if r.Method == "GET" {
		if collection {
			list, err := s.db.ListSecretReferences(r.Context(), s.workspaceID, actorID)
			if err != nil {
				repositoryFailure(w, err)
				return
			}
			apiJSON(w, 200, struct {
				References []workspace.SecretReference `json:"references"`
			}{list})
			return
		}
		ref, err := s.db.SecretReferenceMetadata(r.Context(), s.workspaceID, actorID, id)
		if err != nil {
			repositoryFailure(w, err)
			return
		}
		w.Header().Set("ETag", secretETag(ref))
		apiJSON(w, 200, ref)
		return
	}
	if r.Method == "POST" && !collection || r.Method == "PUT" && collection {
		apiFailure(w, 405, "Use POST on the collection or PUT on a reference.")
		return
	}
	var revision int64
	if r.Method == "PUT" {
		tag := r.Header.Get("If-Match")
		if tag == "" {
			apiFailure(w, http.StatusPreconditionRequired, "Supply the reference ETag in If-Match.")
			return
		}
		prefix := `"secret/` + id + `/`
		if !strings.HasPrefix(tag, prefix) || !strings.HasSuffix(tag, `"`) {
			apiFailure(w, 400, "Invalid reference ETag.")
			return
		}
		text := strings.TrimSuffix(strings.TrimPrefix(tag, prefix), `"`)
		var err error
		revision, err = strconv.ParseInt(text, 10, 64)
		if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != text {
			apiFailure(w, 400, "Invalid reference ETag.")
			return
		}
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		apiFailure(w, http.StatusUnsupportedMediaType, "Use application/json.")
		return
	}
	var input secretInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		apiFailure(w, 400, "Invalid reference metadata. Secret values are not accepted.")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		apiFailure(w, 400, "Supply one reference object.")
		return
	}
	var ref workspace.SecretReference
	if r.Method == "POST" {
		if input.Enabled != nil {
			apiFailure(w, 400, "New references are enabled; use PUT to change their state.")
			return
		}
		ref, err = s.db.CreateSecretReference(r.Context(), s.workspaceID, actorID, input.Name, input.Key)
	} else {
		if input.Enabled == nil {
			apiFailure(w, 400, "Specify enabled when updating a reference.")
			return
		}
		ref, err = s.db.UpdateSecretReference(r.Context(), s.workspaceID, actorID, id, revision, input.Name, input.Key, *input.Enabled)
	}
	if err != nil {
		repositoryFailure(w, err)
		return
	}
	w.Header().Set("ETag", secretETag(ref))
	status := http.StatusOK
	if r.Method == "POST" {
		status = http.StatusCreated
		w.Header().Set("Location", "/api/secret-references/"+ref.ID)
	}
	apiJSON(w, status, ref)
}
func repositoryFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sharedtest.ErrInvalid):
		apiFailure(w, 400, "Invalid shared test. Use public_config and secret references with empty destination fields.")
	case errors.Is(err, workspace.ErrDraftLimit):
		apiFailure(w, 409, "You have 10 drafts. Remove an old draft before creating another.")
	case errors.Is(err, workspace.ErrForbidden):
		apiFailure(w, 403, "Workspace action is not permitted.")
	case errors.Is(err, workspace.ErrConflict):
		apiFailure(w, 409, "Resource changed or its name is already in use. Retain your draft and reload.")
	case errors.Is(err, workspace.ErrNotFound):
		apiFailure(w, 404, "Resource not found.")
	case errors.Is(err, workspace.ErrUnavailable):
		apiFailure(w, 503, "Workspace is temporarily unavailable.")
	default:
		apiFailure(w, 400, "Invalid reference or request metadata.")
	}
}
func apiJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func apiFailure(w http.ResponseWriter, status int, message string) {
	apiJSON(w, status, struct {
		Error string `json:"error"`
	}{message})
}
