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
	mu           sync.Mutex
	file         string
	outputDir    string
	projects     map[string]flow.Project
	artifacts    map[string]map[string]flow.Artifact
	publications map[string]map[string]flow.Publication
}

type savedEnvelope struct {
	Projects     map[string]flow.Project                `json:"projects"`
	Artifacts    map[string]map[string]flow.Artifact    `json:"artifacts"`
	Publications map[string]map[string]flow.Publication `json:"publications"`
}

func newStore(file string, outputDir string) (*store, error) {
	s := &store{file: file, outputDir: outputDir, projects: map[string]flow.Project{}, artifacts: map[string]map[string]flow.Artifact{}, publications: map[string]map[string]flow.Publication{}}
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
	if envelope.Publications != nil {
		s.publications = envelope.Publications
	}
	// 旧数据里 nodes/edges 可能是 null：归一化为 []，避免前端“未定义节点集合”崩溃。
	for id := range s.projects {
		project := s.projects[id]
		project.Normalize()
		s.projects[id] = project
	}
	for id := range s.artifacts {
		if s.artifacts[id] == nil {
			s.artifacts[id] = map[string]flow.Artifact{}
		}
	}
	for id := range s.publications {
		if s.publications[id] == nil {
			s.publications[id] = map[string]flow.Publication{}
		}
	}
	return s, nil
}

func (s *store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(savedEnvelope{Projects: s.projects, Artifacts: s.artifacts, Publications: s.publications}, "", "  ")
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
	mux := buildMux(store, staticDir())
	address := getenv("DATAFLOW_ADDR", ":8080")
	log.Printf("dataflow server listening on %s", address)
	if err := http.ListenAndServe(address, mux); err != nil {
		log.Fatal(err)
	}
}

func buildMux(store *store, staticDir string) *http.ServeMux {
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
		// 空工程也要保存：nodes/edges 归一化为 [] 而不是 null。
		project.Normalize()
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
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "保存失败：工程未替换，可在当前画布继续编辑后重试。" + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"project": project, "validation": validation, "publications": store.publications[project.ID]})
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
		artifacts := store.artifacts[id]
		if artifacts == nil {
			artifacts = map[string]flow.Artifact{}
		}
		publications := store.publications[id]
		if publications == nil {
			publications = map[string]flow.Publication{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"project": project, "artifacts": artifacts, "publications": publications})
	})
	mux.HandleFunc("POST /api/projects/{id}/run", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		var body struct {
			StartNodeIDs  []string `json:"startNodeIds"`
			PublishPolicy string   `json:"publishPolicy"`
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
		project.Normalize()
		validation := flow.Validate(project)
		if !validation.OK {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "issues": validation.Issues, "executionOrder": []string{}, "affectedNodeIds": []string{}, "results": map[string]flow.NodeResult{}, "publications": store.publications[id]})
			return
		}
		runID := newRunID()
		policy := body.PublishPolicy
		if policy != flow.PublishAll {
			policy = flow.PublishPartial
		}
		result := flow.RunWithOptions(project, runID, flow.Options{
			StartNodeIDs: body.StartNodeIDs,
			Stored:       store.artifacts[id],
			Publications: store.publications[id],
			Policy:       policy,
			OutputRoot:   filepath.Join(store.outputDir, id),
			Now:          time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		})
		if store.artifacts[id] == nil {
			store.artifacts[id] = map[string]flow.Artifact{}
		}
		if store.publications[id] == nil {
			store.publications[id] = map[string]flow.Publication{}
		}
		for nodeID, nodeResult := range result.Results {
			if nodeResult.Artifact != nil && nodeResult.Status == "succeeded" {
				store.artifacts[id][nodeID] = *nodeResult.Artifact
			}
			if nodeResult.Status == "failed" || nodeResult.Status == "blocked" {
				delete(store.artifacts[id], nodeID)
			}
		}
		// 发布凭证只追加/替换为已确认内容，失败不会清除已发布证据。
		for nodeID, publication := range result.Publications {
			store.publications[id][nodeID] = publication
		}
		if err := store.saveLocked(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "结果已计算但落盘失败：请保留当前画布重试，已发布凭证未被删除。" + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	if staticDir != "" {
		mux.Handle("GET /", spaHandler(staticDir))
	}
	return mux
}

func staticDir() string {
	if dir := os.Getenv("DATAFLOW_STATIC"); dir != "" {
		return dir
	}
	for _, candidate := range []string{"frontend/dist", "../frontend/dist", "dist"} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func spaHandler(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean("/" + r.URL.Path)
		if info, err := os.Stat(filepath.Join(dir, clean)); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
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
