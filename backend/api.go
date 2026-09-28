package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func RegisterRoutes(mux *http.ServeMux, store *Store) {
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			store.mu.Lock()
			defer store.mu.Unlock()
			list := []Project{}
			for _, p := range store.Projects {
				list = append(list, p)
			}
			writeJSON(w, list)
			return
		}
		if r.Method == http.MethodPost {
			var p Project
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			p.SchemaVersion = "workflow/v1"
			if err := ValidateProject(p); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if old, ok := store.Projects[p.ID]; ok {
				p.Version = old.Version + 1
			} else {
				p.Version = 1
			}
			store.Projects[p.ID] = p
			if err := store.SaveLocked(); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, SaveProjectResponse{Project: p})
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[len("/api/projects/"):]
		store.mu.Lock()
		p, ok := store.Projects[id]
		store.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, p)
	})
	mux.HandleFunc("/api/runs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			store.mu.Lock()
			defer store.mu.Unlock()
			list := []Run{}
			for _, run := range store.Runs {
				list = append(list, run)
			}
			writeJSON(w, list)
			return
		}
		var req StartRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		run, err := store.StartRun(req.ProjectID, req.FromNodeID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, run)
	})
	mux.HandleFunc("/api/runs/", func(w http.ResponseWriter, r *http.Request) {
		parts := splitPath(r.URL.Path)
		if len(parts) != 3 || parts[0] != "runs" || parts[2] != "decision" {
			http.NotFound(w, r)
			return
		}
		var req DecisionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		run, err := store.Decide(parts[1], req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, run)
	})
}

func splitPath(p string) []string {
	out, cur := []string{}, ""
	for _, c := range p {
		if c == '/' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
		} else {
			cur += string(c)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out[1:]
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	http.Error(w, fmt.Sprintf("{\"error\":%q}", err.Error()), code)
}
