// Package api exposes the HTTP JSON API and serves the web page.
package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"github.com/ilyasslefhaili/reachly/internal/finder"
	"github.com/ilyasslefhaili/reachly/internal/jobs"
)

type Server struct {
	Q   *jobs.Queue
	Web fs.FS
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/lookup", s.lookup)
	mux.HandleFunc("POST /api/lookup/bulk", s.bulk)
	mux.HandleFunc("GET /api/jobs/{id}", s.job)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("GET /", http.FileServerFS(s.Web))
	return mux
}

type lookupReq struct {
	Name    string `json:"name"`
	Company string `json:"company"`
}

func (s *Server) lookup(w http.ResponseWriter, r *http.Request) {
	var req lookupReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Name, req.Company = strings.TrimSpace(req.Name), strings.TrimSpace(req.Company)
	if req.Name == "" || req.Company == "" {
		writeErr(w, http.StatusBadRequest, "name and company are required")
		return
	}
	j, err := s.Q.Submit(req.Name, req.Company)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": j.ID})
}

// bulk accepts {"lines": "Jean Dupont, acme.com\nJane Doe, Globex"} — one lookup per line.
func (s *Server) bulk(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Lines string `json:"lines"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	type item struct {
		JobID   string `json:"job_id,omitempty"`
		Name    string `json:"name"`
		Company string `json:"company"`
		Error   string `json:"error,omitempty"`
	}
	var out []item
	for _, line := range strings.Split(req.Lines, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, company, perr := finder.ParseLine(line)
		it := item{Name: name, Company: company}
		if perr != nil {
			it.Error = perr.Error()
		} else if j, err := s.Q.Submit(it.Name, it.Company); err != nil {
			it.Error = err.Error()
		} else {
			it.JobID = j.ID
		}
		out = append(out, it)
		if len(out) >= 500 {
			break
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"jobs": out})
}

func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	j, ok := s.Q.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
