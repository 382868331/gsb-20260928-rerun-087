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
	mu         sync.Mutex
	file       string
	outputDir  string
	projects   map[string]flow.Project
	artifacts  map[string]map[string]flow.Artifact
	deliveries map[string]map[string]flow.DeliveryState
	candidates map[string]map[string]flow.Candidate
	views      map[string][]flow.CompareView
}

type savedEnvelope struct {
	Projects   map[string]flow.Project                  `json:"projects"`
	Artifacts  map[string]map[string]flow.Artifact      `json:"artifacts"`
	Deliveries map[string]map[string]flow.DeliveryState `json:"deliveries"`
	Candidates map[string]map[string]flow.Candidate     `json:"candidates"`
	Views      map[string][]flow.CompareView            `json:"views"`
}

func newStore(file string, outputDir string) (*store, error) {
	s := &store{file: file, outputDir: outputDir, projects: map[string]flow.Project{}, artifacts: map[string]map[string]flow.Artifact{}, deliveries: map[string]map[string]flow.DeliveryState{}, candidates: map[string]map[string]flow.Candidate{}, views: map[string][]flow.CompareView{}}
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
	if envelope.Deliveries != nil {
		s.deliveries = envelope.Deliveries
	}
	if envelope.Candidates != nil {
		s.candidates = envelope.Candidates
	}
	if envelope.Views != nil {
		s.views = envelope.Views
	}
	// 磁盘上的旧数据可能把空节点/连接集合存成 null，重新打开时统一归一化为 []，
	// 避免前端拿到未定义的节点集合后整页崩溃。
	for id, project := range s.projects {
		s.projects[id] = normalizeProject(project)
	}
	return s, nil
}

func (s *store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(savedEnvelope{Projects: s.projects, Artifacts: s.artifacts, Deliveries: s.deliveries, Candidates: s.candidates, Views: s.views}, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.file + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, s.file)
}

func normalizeProject(project flow.Project) flow.Project {
	if project.Nodes == nil {
		project.Nodes = []flow.Node{}
	}
	if project.Edges == nil {
		project.Edges = []flow.Edge{}
	}
	for index, node := range project.Nodes {
		if node.Params == nil {
			project.Nodes[index].Params = map[string]any{}
		}
	}
	return project
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
		// 空工程 / 空连接集合在 JSON 里可能是 null，落库前统一为 []，
		// 保证保存响应与重新打开都返回可立即继续编辑的节点集合。
		project = normalizeProject(project)
		store.mu.Lock()
		defer store.mu.Unlock()
		validation := flow.Validate(project)
		if !validation.OK {
			writeJSON(w, http.StatusUnprocessableEntity, validation)
			return
		}
		project = normalizeProject(project)
		if existing, ok := store.projects[project.ID]; ok {
			project.Revision = existing.Revision + 1
		} else {
			project.Revision = 1
		}
		store.projects[project.ID] = project
		// 工程内容一旦变化，依据旧修订计算的待确认候选立即永久失效并持久化：
		// 失效状态不存在于单次请求判断里，之后恢复原文也不能恢复其发布资格。
		if store.deliveries[project.ID] != nil {
			store.deliveries[project.ID] = flow.InvalidatePendingForProject(store.deliveries[project.ID], flow.ProjectVersion(project))
		}
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
		writeJSON(w, http.StatusOK, map[string]any{"project": project, "artifacts": store.artifacts[id], "deliveries": store.deliveries[id]})
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
		result := flow.Run(project, runID, body.StartNodeIDs, store.artifacts[id], store.deliveries[id], filepath.Join(store.outputDir, id, runID))
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
		// 投递状态（含已发布证据）随运行推进并持久化；失败/阻断不会清除已发出的结果。
		if result.Deliveries != nil {
			store.deliveries[id] = result.Deliveries
		}
		// 归档本轮新产生的发布候选（跨运行对比用）：追加式持久化，
		// 输出相同但依据不同的候选各自独立登记，互不合并。
		if len(result.Candidates) > 0 {
			if store.candidates[id] == nil {
				store.candidates[id] = map[string]flow.Candidate{}
			}
			for _, candidate := range result.Candidates {
				if _, exists := store.candidates[id][candidate.Key]; !exists {
					store.candidates[id][candidate.Key] = candidate
				}
			}
		}
		if err := store.saveLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /api/projects/{id}/confirm", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		var body struct {
			NodeIDs  []string                 `json:"nodeIds"`
			All      bool                     `json:"all"`
			Expected []flow.ExpectedCandidate `json:"expected"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "确认请求 JSON 无效")
			return
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		project, ok := store.projects[id]
		if !ok {
			writeError(w, http.StatusNotFound, "请先保存工程")
			return
		}
		if store.deliveries[id] == nil {
			store.deliveries[id] = map[string]flow.DeliveryState{}
		}
		updated, confirmed, err := flow.ConfirmDeliveries(store.deliveries[id], body.NodeIDs, body.All, body.Expected, newRunID(), flow.ProjectVersion(project))
		if err != nil {
			// 拒绝也要持久化：失效标记与拒绝证据落盘，恢复原文不能恢复发布资格。
			store.deliveries[id] = updated
			if saveErr := store.saveLocked(); saveErr != nil {
				writeError(w, http.StatusInternalServerError, saveErr.Error())
				return
			}
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "deliveries": updated})
			return
		}
		store.deliveries[id] = updated
		if err := store.saveLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"confirmed": confirmed, "deliveries": updated})
	})
	// 跨运行候选对比：筛选、并排对比与筛选视图保存都是只读操作——
	// 不改变接收方当前可见版本，也不恢复已失效候选的发布资格。
	mux.HandleFunc("GET /api/projects/{id}/candidates", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		store.mu.Lock()
		defer store.mu.Unlock()
		if _, ok := store.projects[id]; !ok {
			writeError(w, http.StatusNotFound, "工程不存在")
			return
		}
		query := request.URL.Query()
		filter := flow.CandidateFilter{
			Receiver:       query.Get("receiver"),
			ProjectVersion: query.Get("projectVersion"),
			RunFrom:        query.Get("runFrom"),
			RunTo:          query.Get("runTo"),
			Status:         query.Get("status"),
		}
		writeJSON(w, http.StatusOK, map[string]any{"candidates": flow.ListCandidates(store.candidates[id], store.deliveries[id], filter)})
	})
	mux.HandleFunc("POST /api/projects/{id}/compare", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		var body struct {
			A string `json:"a"`
			B string `json:"b"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "对比请求 JSON 无效")
			return
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if _, ok := store.projects[id]; !ok {
			writeError(w, http.StatusNotFound, "工程不存在")
			return
		}
		a, okA := store.candidates[id][body.A]
		b, okB := store.candidates[id][body.B]
		if !okA || !okB {
			writeError(w, http.StatusNotFound, "候选不存在或已被清理")
			return
		}
		writeJSON(w, http.StatusOK, flow.CompareCandidates(a, b, store.deliveries[id]))
	})
	mux.HandleFunc("GET /api/projects/{id}/compare-views", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		store.mu.Lock()
		defer store.mu.Unlock()
		if _, ok := store.projects[id]; !ok {
			writeError(w, http.StatusNotFound, "工程不存在")
			return
		}
		views := store.views[id]
		if views == nil {
			views = []flow.CompareView{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"views": views})
	})
	mux.HandleFunc("POST /api/projects/{id}/compare-views", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		var view flow.CompareView
		if err := json.NewDecoder(request.Body).Decode(&view); err != nil {
			writeError(w, http.StatusBadRequest, "视图 JSON 无效")
			return
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if _, ok := store.projects[id]; !ok {
			writeError(w, http.StatusNotFound, "工程不存在")
			return
		}
		updated, err := flow.SaveCompareView(store.views[id], view)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		store.views[id] = updated
		if err := store.saveLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"views": updated})
	})
	mux.HandleFunc("DELETE /api/projects/{id}/compare-views/{name}", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		name := request.PathValue("name")
		store.mu.Lock()
		defer store.mu.Unlock()
		if _, ok := store.projects[id]; !ok {
			writeError(w, http.StatusNotFound, "工程不存在")
			return
		}
		store.views[id] = flow.DeleteCompareView(store.views[id], name)
		if err := store.saveLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"views": store.views[id]})
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
