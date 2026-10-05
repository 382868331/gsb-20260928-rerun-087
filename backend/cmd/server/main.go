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
	mu                 sync.Mutex
	file               string
	outputDir          string
	projects           map[string]flow.Project
	artifacts          map[string]map[string]flow.Artifact
	deliveries         map[string]map[string]flow.DeliveryState
	groups             map[string]map[string]flow.PublishGroup
	candidateArchives  map[string]map[string]flow.CandidateArchive
	comparisonSettings map[string]flow.CandidateComparisonState
}

type savedEnvelope struct {
	Projects           map[string]flow.Project                     `json:"projects"`
	Artifacts          map[string]map[string]flow.Artifact         `json:"artifacts"`
	Deliveries         map[string]map[string]flow.DeliveryState    `json:"deliveries"`
	Groups             map[string]map[string]flow.PublishGroup     `json:"groups"`
	CandidateArchives  map[string]map[string]flow.CandidateArchive `json:"candidateArchives"`
	ComparisonSettings map[string]flow.CandidateComparisonState    `json:"comparisonSettings"`
}

func newStore(file string, outputDir string) (*store, error) {
	s := &store{file: file, outputDir: outputDir, projects: map[string]flow.Project{}, artifacts: map[string]map[string]flow.Artifact{}, deliveries: map[string]map[string]flow.DeliveryState{}, groups: map[string]map[string]flow.PublishGroup{}, candidateArchives: map[string]map[string]flow.CandidateArchive{}, comparisonSettings: map[string]flow.CandidateComparisonState{}}
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
	if envelope.Groups != nil {
		s.groups = envelope.Groups
	}
	if envelope.CandidateArchives != nil {
		s.candidateArchives = envelope.CandidateArchives
	}
	if envelope.ComparisonSettings != nil {
		s.comparisonSettings = envelope.ComparisonSettings
	}
	// 磁盘上的旧数据可能把空节点/连接集合存成 null，重新打开时统一归一化为 []，
	// 避免前端拿到未定义的节点集合后整页崩溃。
	for id, project := range s.projects {
		s.projects[id] = normalizeProject(project)
		flow.ReconcileCandidateArchives(s.deliveries[id], s.candidateArchives[id])
	}
	// 上次进程退出时停留在提交中的发布组必须恢复到确定结局：
	// 重新打开（页面刷新）后重放幂等提交，不会重复发布，也不会永远卡在提交中。
	recovered := false
	for id, projectGroups := range s.groups {
		project, exists := s.projects[id]
		if !exists {
			continue
		}
		if s.deliveries[id] == nil {
			s.deliveries[id] = map[string]flow.DeliveryState{}
		}
		if s.candidateArchives[id] == nil {
			s.candidateArchives[id] = map[string]flow.CandidateArchive{}
		}
		for groupID, group := range projectGroups {
			if group.Status != flow.GroupStatusCommitting {
				continue
			}
			updated, _, err := flow.RecoverGroup(s.deliveries[id], projectGroups, s.candidateArchives[id], groupID, flow.ProjectVersion(project))
			if err == nil {
				s.deliveries[id] = updated
				recovered = true
			}
		}
	}
	if recovered {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(savedEnvelope{Projects: s.projects, Artifacts: s.artifacts, Deliveries: s.deliveries, Groups: s.groups, CandidateArchives: s.candidateArchives, ComparisonSettings: s.comparisonSettings}, "", "  ")
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

func archiveList(archives map[string]flow.CandidateArchive) []flow.CandidateArchive {
	list := make([]flow.CandidateArchive, 0, len(archives))
	for _, archive := range archives {
		list = append(list, archive)
	}
	return list
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
			flow.MarkPendingArchivesStale(store.deliveries[project.ID], store.candidateArchives[project.ID])
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
		writeJSON(w, http.StatusOK, map[string]any{"project": project, "artifacts": store.artifacts[id], "deliveries": store.deliveries[id], "groups": store.groups[id], "candidateArchives": archiveList(store.candidateArchives[id]), "comparison": store.comparisonSettings[id]})
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
		if store.candidateArchives[id] == nil {
			store.candidateArchives[id] = map[string]flow.CandidateArchive{}
		}
		result := flow.Run(project, runID, body.StartNodeIDs, store.artifacts[id], store.deliveries[id], filepath.Join(store.outputDir, id, runID), store.candidateArchives[id])
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
		archiveMap := map[string]flow.CandidateArchive{}
		for _, archive := range result.CandidateArchives {
			archiveMap[archive.Key] = archive
		}
		store.candidateArchives[id] = archiveMap
		if err := store.saveLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run": result, "candidateArchives": result.CandidateArchives})
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
		if store.candidateArchives[id] == nil {
			store.candidateArchives[id] = map[string]flow.CandidateArchive{}
		}
		updated, confirmed, err := flow.ConfirmDeliveries(store.deliveries[id], body.NodeIDs, body.All, body.Expected, newRunID(), flow.ProjectVersion(project))
		if err != nil {
			// 拒绝也要持久化：失效标记与拒绝证据落盘，恢复原文不能恢复发布资格。
			store.deliveries[id] = updated
			flow.MarkPendingArchivesStale(updated, store.candidateArchives[id])
			if saveErr := store.saveLocked(); saveErr != nil {
				writeError(w, http.StatusInternalServerError, saveErr.Error())
				return
			}
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "deliveries": updated, "candidateArchives": archiveList(store.candidateArchives[id])})
			return
		}
		store.deliveries[id] = updated
		for _, nodeID := range confirmed {
			if state, exists := updated[nodeID]; exists && state.Published != nil {
				flow.MarkArchivePublished(store.candidateArchives[id], *state.Published)
			}
		}
		if err := store.saveLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"confirmed": confirmed, "deliveries": updated, "candidateArchives": archiveList(store.candidateArchives[id])})
	})
	mux.HandleFunc("POST /api/projects/{id}/groups", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		var body struct {
			Members      []flow.PublishGroupMember     `json:"members"`
			Dependencies []flow.PublishGroupDependency `json:"dependencies"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "发布组请求 JSON 无效")
			return
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		project, ok := store.projects[id]
		if !ok {
			writeError(w, http.StatusNotFound, "请先保存工程")
			return
		}
		if store.groups[id] == nil {
			store.groups[id] = map[string]flow.PublishGroup{}
		}
		group, err := flow.PrepareGroup(store.deliveries[id], "grp-"+newRunID(), body.Members, body.Dependencies, flow.ProjectVersion(project))
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "groups": store.groups[id]})
			return
		}
		store.groups[id][group.ID] = group
		if err := store.saveLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"group": group, "groups": store.groups[id]})
	})
	mux.HandleFunc("POST /api/projects/{id}/groups/{gid}/commit", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		gid := request.PathValue("gid")
		var body struct {
			UnavailableReceivers []string `json:"unavailableReceivers"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "提交请求 JSON 无效")
			return
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		project, ok := store.projects[id]
		if !ok {
			writeError(w, http.StatusNotFound, "请先保存工程")
			return
		}
		if store.groups[id] == nil {
			store.groups[id] = map[string]flow.PublishGroup{}
		}
		if store.deliveries[id] == nil {
			store.deliveries[id] = map[string]flow.DeliveryState{}
		}
		if store.candidateArchives[id] == nil {
			store.candidateArchives[id] = map[string]flow.CandidateArchive{}
		}
		unavailable := map[string]bool{}
		for _, nodeID := range body.UnavailableReceivers {
			unavailable[nodeID] = true
		}
		updated, group, err := flow.CommitGroup(store.deliveries[id], store.groups[id], store.candidateArchives[id], gid, unavailable, flow.ProjectVersion(project))
		store.deliveries[id] = updated
		// 无论成功、冲突还是中途不可用，当前确定状态（含 committing）都先落盘：
		// 页面刷新或进程重启后据此恢复，不会重复发布。
		if saveErr := store.saveLocked(); saveErr != nil {
			writeError(w, http.StatusInternalServerError, saveErr.Error())
			return
		}
		payload := map[string]any{"group": group, "groups": store.groups[id], "deliveries": store.deliveries[id], "candidateArchives": archiveList(store.candidateArchives[id])}
		if err != nil {
			payload["error"] = err.Error()
			status := http.StatusConflict
			if group.Status == flow.GroupStatusCommitting {
				status = http.StatusServiceUnavailable
			}
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, payload)
	})
	mux.HandleFunc("POST /api/projects/{id}/groups/{gid}/recover", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		gid := request.PathValue("gid")
		store.mu.Lock()
		defer store.mu.Unlock()
		project, ok := store.projects[id]
		if !ok {
			writeError(w, http.StatusNotFound, "请先保存工程")
			return
		}
		if store.groups[id] == nil {
			store.groups[id] = map[string]flow.PublishGroup{}
		}
		if store.deliveries[id] == nil {
			store.deliveries[id] = map[string]flow.DeliveryState{}
		}
		if store.candidateArchives[id] == nil {
			store.candidateArchives[id] = map[string]flow.CandidateArchive{}
		}
		updated, group, err := flow.RecoverGroup(store.deliveries[id], store.groups[id], store.candidateArchives[id], gid, flow.ProjectVersion(project))
		store.deliveries[id] = updated
		if saveErr := store.saveLocked(); saveErr != nil {
			writeError(w, http.StatusInternalServerError, saveErr.Error())
			return
		}
		payload := map[string]any{"group": group, "groups": store.groups[id], "deliveries": store.deliveries[id], "candidateArchives": archiveList(store.candidateArchives[id])}
		if err != nil {
			payload["error"] = err.Error()
			writeJSON(w, http.StatusConflict, payload)
			return
		}
		writeJSON(w, http.StatusOK, payload)
	})
	mux.HandleFunc("POST /api/projects/{id}/groups/{gid}/cancel", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		gid := request.PathValue("gid")
		store.mu.Lock()
		defer store.mu.Unlock()
		if _, ok := store.projects[id]; !ok {
			writeError(w, http.StatusNotFound, "请先保存工程")
			return
		}
		if store.groups[id] == nil {
			store.groups[id] = map[string]flow.PublishGroup{}
		}
		group, err := flow.CancelGroup(store.groups[id], gid)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "group": group, "groups": store.groups[id]})
			return
		}
		if err := store.saveLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"group": group, "groups": store.groups[id]})
	})
	mux.HandleFunc("PUT /api/projects/{id}/candidate-comparison", func(w http.ResponseWriter, request *http.Request) {
		id := request.PathValue("id")
		var state flow.CandidateComparisonState
		if err := json.NewDecoder(request.Body).Decode(&state); err != nil {
			writeError(w, http.StatusBadRequest, "对比设置 JSON 无效")
			return
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if _, exists := store.projects[id]; !exists {
			writeError(w, http.StatusNotFound, "工程不存在")
			return
		}
		store.comparisonSettings[id] = state
		if err := store.saveLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"comparison": state})
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
