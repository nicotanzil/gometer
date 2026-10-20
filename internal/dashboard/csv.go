package dashboard

import (
	"encoding/json"
	"gometer/internal/config"
	"io"
	"net/http"
	"strings"
)

func (s *Server) csvAPI(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/csv/preview" && r.URL.Path != "/api/csv/validate" {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "Method not allowed.")
		return true
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 400, "Send CSV source as JSON.")
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, config.MaxConfigBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var c config.CSV
	if err := dec.Decode(&c); err != nil {
		fail(w, 400, "Read CSV source: "+err.Error())
		return true
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		fail(w, 400, "Send one CSV source object.")
		return true
	}
	if r.URL.Path == "/api/csv/validate" {
		if _, err := config.CompileCSV(c); err != nil {
			fail(w, 400, err.Error())
			return true
		}
	}
	preview, err := config.PreviewCSV(c.Content, c.Delimiter)
	if err != nil {
		fail(w, 400, err.Error())
		return true
	}
	jsonResponse(w, 200, preview)
	return true
}
