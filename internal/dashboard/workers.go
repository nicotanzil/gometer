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
	"unicode"

	"gometer/internal/config"
	"gometer/internal/distributed"
)

type SavedWorker struct {
	ID        string                   `json:"id"`
	Name      string                   `json:"name"`
	Origin    string                   `json:"origin"`
	UpdatedAt time.Time                `json:"updated_at"`
	Check     *distributed.WorkerCheck `json:"check,omitempty"`
}

// WorkerSnapshot freezes catalog identity alongside the actual submitted origin.
// Runs never retain a mutable reference to the catalog.
type WorkerSnapshot struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Origin string `json:"origin"`
}

func validWorkerName(name string) bool {
	return name == strings.TrimSpace(name) && name != "" && len(name) <= 256 && !strings.ContainsFunc(name, unicode.IsControl)
}

func (s *store) workerList() []SavedWorker {
	list := make([]SavedWorker, 0, len(s.workers))
	for _, worker := range s.workers {
		list = append(list, worker)
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name)
		if a == b {
			return list[i].ID < list[j].ID
		}
		return a < b
	})
	return list
}

// Caller holds s.mu. Origins, not IDs, remain the execution configuration.
func (s *Server) snapshotWorkers(origins []string) []WorkerSnapshot {
	if len(origins) == 0 {
		return nil
	}
	list := make([]WorkerSnapshot, 0, len(origins))
	for _, origin := range origins {
		snapshot := WorkerSnapshot{Name: origin, Origin: origin}
		key, _ := config.NormalizeWorkerOrigin(origin)
		for _, worker := range s.store.workers {
			if worker.Origin == key {
				snapshot.ID, snapshot.Name = worker.ID, worker.Name
				break
			}
		}
		list = append(list, snapshot)
	}
	return list
}

func validateWorkerSnapshots(snapshots []WorkerSnapshot, origins []string) error {
	if len(snapshots) == 0 {
		return nil
	}
	if len(snapshots) != len(origins) || len(snapshots) > config.MaxWorkers {
		return fmt.Errorf("worker snapshots must match the run's worker origins")
	}
	for i, snapshot := range snapshots {
		if snapshot.Origin != origins[i] || (snapshot.ID != "" && !validID(snapshot.ID)) || (!validWorkerName(snapshot.Name) && snapshot.Name != snapshot.Origin) {
			return fmt.Errorf("invalid worker snapshot at index %d", i)
		}
	}
	return nil
}

func readWorker(w http.ResponseWriter, r *http.Request) (SavedWorker, error) {
	var input struct {
		Name   string `json:"name"`
		Origin string `json:"origin"`
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return SavedWorker{}, fmt.Errorf("send a worker name and origin as JSON")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return SavedWorker{}, fmt.Errorf("send a worker object containing only name and origin")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return SavedWorker{}, fmt.Errorf("send one worker object")
	}
	input.Name = strings.TrimSpace(input.Name)
	if !validWorkerName(input.Name) {
		return SavedWorker{}, fmt.Errorf("worker name must contain 1–256 bytes without control characters")
	}
	origin, err := config.NormalizeWorkerOrigin(strings.TrimSpace(input.Origin))
	if err != nil {
		return SavedWorker{}, err
	}
	return SavedWorker{Name: input.Name, Origin: origin, UpdatedAt: time.Now().UTC()}, nil
}

func (s *Server) workersAPI(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/api/workers" && !strings.HasPrefix(path, "/api/workers/") {
		return false
	}
	tail := strings.TrimPrefix(path, "/api/workers")
	parts := strings.Split(strings.TrimPrefix(tail, "/"), "/")
	id := ""
	if tail != "" {
		id = parts[0]
		if !validID(id) || len(parts) > 2 || (len(parts) == 2 && parts[1] != "check") {
			fail(w, 404, "Worker not found.")
			return true
		}
	}
	if id != "" && len(parts) == 2 {
		if r.Method != "POST" {
			fail(w, 405, "Method not allowed.")
		} else {
			s.checkWorker(w, r, id)
		}
		return true
	}
	if r.Method == "GET" && id == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		jsonResponse(w, 200, s.store.workerList())
		return true
	}
	if (r.Method == "POST" && id == "") || (r.Method == "PUT" && id != "") {
		worker, err := readWorker(w, r)
		if err != nil {
			fail(w, 400, err.Error())
			return true
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if id != "" {
			previous, ok := s.store.workers[id]
			if !ok {
				fail(w, 404, "Worker not found.")
				return true
			}
			if previous.Origin == worker.Origin {
				worker.Check = previous.Check
			}
		}
		for existingID, existing := range s.store.workers {
			if existingID != id && existing.Origin == worker.Origin {
				fail(w, 409, "This worker origin is already registered.")
				return true
			}
		}
		code := 200
		if id == "" {
			if len(s.store.workers) >= maxSavedWorkers {
				fail(w, 409, "Worker catalog is full (200).")
				return true
			}
			id, err = newID()
			if err != nil {
				fail(w, 500, "Could not create a worker ID.")
				return true
			}
			code = 201
		}
		worker.ID = id
		if err = s.store.write("workers", id, worker); err != nil {
			fail(w, 500, "Could not save the worker. Check workspace permissions and free space.")
			return true
		}
		s.store.workers[id] = worker
		jsonResponse(w, code, worker)
		return true
	}
	if r.Method == "DELETE" && id != "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.store.workers[id]; !ok {
			fail(w, 404, "Worker not found.")
			return true
		}
		if err := os.Remove(filepath.Join(s.store.dir, "workers", id+".json")); err != nil {
			fail(w, 500, "Could not remove the saved worker.")
			return true
		}
		delete(s.store.workers, id)
		jsonResponse(w, 200, map[string]bool{"deleted": true})
		return true
	}
	fail(w, 405, "Method not allowed.")
	return true
}

func (s *Server) checkWorker(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	worker, ok := s.store.workers[id]
	if !ok {
		s.mu.Unlock()
		fail(w, 404, "Worker not found.")
		return
	}
	if s.workerChecking[id] {
		s.mu.Unlock()
		fail(w, 409, "A check is already in progress for this worker.")
		return
	}
	if len(s.workerChecking) >= 8 {
		s.mu.Unlock()
		fail(w, 429, "Eight connection checks are already in progress. Try again shortly.")
		return
	}
	s.workerChecking[id] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.workerChecking, id); s.mu.Unlock() }()
	check := distributed.CheckWorker(r.Context(), worker.Origin, distributed.Options{Token: s.workerToken, CAFile: s.workerCA, AllowHTTPWorkers: s.allowHTTPWorkers})
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.store.workers[id]
	if !ok {
		fail(w, 404, "Worker was removed during the check.")
		return
	}
	if current.Origin != worker.Origin {
		fail(w, 409, "Worker origin changed during the check. Check the updated origin again.")
		return
	}
	current.Check = &check
	if err := s.store.write("workers", id, current); err != nil {
		fail(w, 500, "Could not save the connection check.")
		return
	}
	s.store.workers[id] = current
	jsonResponse(w, 200, current)
}
