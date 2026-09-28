package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"dataflow-designer/internal/flow"
)

type store struct {
	mu        sync.Mutex
	file      string
	outputDir string
	projects  map[string]flow.Project
	artifacts map[string]map[string]flow.Artifact
}

type savedEnvelope struct {
	Projects  map[string]flow.Project             `json:"projects"`
	Artifacts map[string]map[string]flow.Artifact `json:"artifacts"`
}

func newStore(file string, outputDir string) (*store, error) {
	s := &store{file: file, outputDir: outputDir, projects: map[string]flow.Project{}, artifacts: map[string]map[string]flow.Artifact{}}
	encoded, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var envelope savedEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return nil, err
	}
	if envelope.Projects != nil {
		s.projects = envelope.Projects
	}
	if envelope.Artifacts != nil {
		s.artifacts = envelope.Artifacts
	}
	return s, nil
}

func (s *store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(savedEnvelope{Projects: s.projects, Artifacts: s.artifacts}, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.file + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, s.file)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}

func main() {
	dataFile := getenv("DATAFLOW_STORE", "data/store.json")
	outputDir := getenv("DATAFLOW_OUTPUTS", "data/outputs")
	store, err := newStore(dataFile, outputDir)
	if err != nil {
		log.Fatal(err)
	}
	mux := buildMux(store)
	address := getenv("DATAFLOW_ADDR", ":8080")
	log.Printf("dataflow server listening on %s", address)
	if err := http.ListenAndServe(address, mux); err != nil {
		log.Fatal(err)
	}
}

func buildMux(store *store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/components", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, flow.Components()) })
	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, _ *http.Request) {
		store.mu.Lock()
		defer store.mu.Unlock()
		projects := make([]flow.Project, 0, len(store.projects))
		for _, project := range store.projects {
			projects = append(projects, project)
		}
		writeJSON(w, http.StatusOK, projects)
	})
	mux.HandleFunc("POST /api/projects", func(w http.ResponseWriter, request *http.Request) {
		var project flow.Project
		if err := json.NewDecoder(request.Body).Decode(&project); err != nil {
			writeError(w, http.StatusBadRequest, "工程 JSON 无效")
			return
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		validation := flow.Validate(project)
		if !validation.OK {
			writeJSON(w, http.StatusUnprocessableEntity, validation)
			return
		}
		if existing, ok := store.projects[project.ID]; ok {
			project.Revision = existing.Revision + 1
		} else {
			project.Revision = 1
		}
		store.projects[project.ID] = project
		if err := store.saveLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"project": project, "validation": validation})
	})
	mux.HandleFunc("GET /api/projects/{id}", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		store.mu.Lock()
		defer store.mu.Unlock()
		project, ok := store.projects[id]
		if !ok {
			writeError(w, http.StatusNotFound, "工程不存在")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"project": project, "artifacts": store.artifacts[id]})
	})
	mux.HandleFunc("POST /api/projects/{id}/run", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		var body struct {
			StartNodeIDs []string `json:"startNodeIds"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "运行请求 JSON 无效")
			return
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		project, ok := store.projects[id]
		if !ok {
			writeError(w, http.StatusNotFound, "请先保存工程")
			return
		}
		validation := flow.Validate(project)
		if !validation.OK {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "issues": validation.Issues})
			return
		}
		runID := newRunID()
		result := flow.Run(project, runID, body.StartNodeIDs, store.artifacts[id], filepath.Join(store.outputDir, id, runID))
		if store.artifacts[id] == nil {
			store.artifacts[id] = map[string]flow.Artifact{}
		}
		for nodeID, nodeResult := range result.Results {
			if nodeResult.Artifact != nil && nodeResult.Status == "succeeded" {
				store.artifacts[id][nodeID] = *nodeResult.Artifact
			}
			if nodeResult.Status == "failed" || nodeResult.Status == "blocked" {
				delete(store.artifacts[id], nodeID)
			}
		}
		if err := store.saveLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	return mux
}

func getenv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func newRunID() string {
	return "run-" + time.Now().UTC().Format("20060102T150405.000000000")
}
