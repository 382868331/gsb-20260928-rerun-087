package flow

import (
	"fmt"
	"sort"
	"time"
)

// 发布组：把一次运行中的若干待确认候选组成一个一致发布事务。
// 组内可以声明接收方之间的版本依赖；整组要么全部对接收方可见，要么全部不可见。

// PublishGroupMember 绑定组内一个接收方在预备时实际看过的候选身份（版本 + 运行批次）。
type PublishGroupMember struct {
	NodeID  string `json:"nodeId"`
	Version string `json:"version"`
	RunID   string `json:"runId"`
}

// PublishGroupDependency 声明接收方之间的版本依赖：
// NodeID 的发布以 DependsOn 已经（或将在本组内）达到 Version 为前提。
type PublishGroupDependency struct {
	NodeID    string `json:"nodeId"`
	DependsOn string `json:"dependsOn"`
	Version   string `json:"version"`
}

// GroupAdoption 记录提交后每个接收方实际采用的候选身份，供恢复与审计核对。
type GroupAdoption struct {
	NodeID  string `json:"nodeId"`
	Version string `json:"version"`
	RunID   string `json:"runId"`
}

const (
	GroupStatusPrepared   = "prepared"
	GroupStatusCommitting = "committing"
	GroupStatusCommitted  = "committed"
	GroupStatusCancelled  = "cancelled"
)

// PublishGroup 是一个多接收方一致发布事务。
// Basis 记录预备时各接收方当前可见版本，作为提交时冲突判定的共同前提。
type PublishGroup struct {
	ID           string                   `json:"id"`
	Members      []PublishGroupMember     `json:"members"`
	Dependencies []PublishGroupDependency `json:"dependencies"`
	Status       string                   `json:"status"`
	Basis        map[string]string        `json:"basis"`
	Adopted      map[string]GroupAdoption `json:"adopted,omitempty"`
	Note         string                   `json:"note,omitempty"`
	CreatedAt    time.Time                `json:"createdAt"`
}

func memberMap(group PublishGroup) map[string]PublishGroupMember {
	members := make(map[string]PublishGroupMember, len(group.Members))
	for _, member := range group.Members {
		members[member.NodeID] = member
	}
	return members
}

// validateGroupMembers 共同校验：所有必需候选齐全、身份与预备时一致、输入依据仍有效。
// 任一条件不满足时返回错误，整组保持未发布。
func validateGroupMembers(deliveries map[string]DeliveryState, group PublishGroup, projectVersion string) error {
	for _, member := range group.Members {
		state, exists := deliveries[member.NodeID]
		if !exists || state.Pending == nil {
			return fmt.Errorf("节点 %s 缺少必需的待确认候选，整组保持未发布", member.NodeID)
		}
		if state.Pending.Version != member.Version || state.Pending.RunID != member.RunID {
			return fmt.Errorf("节点 %s 的候选已更新（预备时为版本 %s / %s，现为版本 %s / %s），整组保持未发布", member.NodeID, member.Version, member.RunID, state.Pending.Version, state.Pending.RunID)
		}
		if reason := rejectReason(state.Pending, projectVersion); reason != "" {
			return fmt.Errorf("节点 %s 的候选输入依据已失效：%s，整组保持未发布", member.NodeID, reason)
		}
	}
	return nil
}

// validateGroupDependencies 校验接收方之间的版本依赖一致：
// 依赖目标在组内时以组内候选版本为准（允许循环依赖，如三接收方互相依赖），
// 在组外时要求该接收方当前可见版本与声明一致。
func validateGroupDependencies(deliveries map[string]DeliveryState, group PublishGroup) error {
	members := memberMap(group)
	for _, dependency := range group.Dependencies {
		if _, ok := members[dependency.NodeID]; !ok {
			return fmt.Errorf("依赖声明的节点 %s 不在发布组内", dependency.NodeID)
		}
		if depended, ok := members[dependency.DependsOn]; ok {
			if depended.Version != dependency.Version {
				return fmt.Errorf("节点 %s 依赖 %s 的版本 %s，但组内候选为版本 %s，依赖版本不一致", dependency.NodeID, dependency.DependsOn, dependency.Version, depended.Version)
			}
			continue
		}
		state, exists := deliveries[dependency.DependsOn]
		if !exists || state.Published == nil {
			return fmt.Errorf("节点 %s 依赖的接收方 %s 尚无已发布版本，依赖前提不成立", dependency.NodeID, dependency.DependsOn)
		}
		if state.Published.Version != dependency.Version {
			return fmt.Errorf("节点 %s 依赖 %s 的版本 %s，但其当前可见版本为 %s，依赖版本不一致", dependency.NodeID, dependency.DependsOn, dependency.Version, state.Published.Version)
		}
	}
	return nil
}

// PrepareGroup 把若干待确认候选组成发布组并记录共同前提（各接收方当前可见版本）。
// 预备前共同校验：候选齐全、身份一致、依据有效、依赖版本一致；任一不满足则整组不成立。
func PrepareGroup(deliveries map[string]DeliveryState, groupID string, members []PublishGroupMember, dependencies []PublishGroupDependency, projectVersion string) (PublishGroup, error) {
	if len(members) < 2 {
		return PublishGroup{}, fmt.Errorf("发布组至少需要两个接收方候选")
	}
	seen := map[string]bool{}
	for _, member := range members {
		if seen[member.NodeID] {
			return PublishGroup{}, fmt.Errorf("节点 %s 在发布组中重复", member.NodeID)
		}
		seen[member.NodeID] = true
	}
	group := PublishGroup{
		ID:           groupID,
		Members:      append([]PublishGroupMember(nil), members...),
		Dependencies: append([]PublishGroupDependency(nil), dependencies...),
		Status:       GroupStatusPrepared,
		Basis:        map[string]string{},
		CreatedAt:    time.Now().UTC(),
	}
	if err := validateGroupMembers(deliveries, group, projectVersion); err != nil {
		return PublishGroup{}, err
	}
	if err := validateGroupDependencies(deliveries, group); err != nil {
		return PublishGroup{}, err
	}
	for _, member := range members {
		visible := ""
		if state, exists := deliveries[member.NodeID]; exists && state.Published != nil {
			visible = state.Published.Version
		}
		group.Basis[member.NodeID] = visible
	}
	return group, nil
}

// checkGroupConflicts 以接收方当前可见版本为共同前提做冲突判定：
// 预备后若别组已发布改变了任一接收方的可见版本，整组拒绝提交——
// 即使本组候选输出内容与该接收方当前可见版本相同，也不能绕过依据变化或覆盖别组新版本。
func checkGroupConflicts(deliveries map[string]DeliveryState, group PublishGroup) error {
	for _, member := range group.Members {
		visible := ""
		if state, exists := deliveries[member.NodeID]; exists && state.Published != nil {
			visible = state.Published.Version
		}
		if visible != group.Basis[member.NodeID] {
			return fmt.Errorf("接收方 %s 的当前可见版本已变为 %s（预备前提为 %s）：与别组发布冲突，即使候选输出内容相同也不能覆盖，整组保持未发布", member.NodeID, visible, group.Basis[member.NodeID])
		}
	}
	return nil
}

// CommitGroup 把发布组从预备推进到完成：
//  1. 重新共同校验候选与依赖，并以预备前提做冲突判定；
//  2. 进入 committing（调用方负责持久化，刷新后据此恢复）；
//  3. 任一接收方暂时不可用时整组停留在 committing，不发布任何成员——
//     读取者只能看到完整旧组或完整新组；
//  4. 全部接收方可用时原子发布整组，记录每个接收方实际采用的候选身份。
//
// 对 committed 组重复提交是幂等成功，不会重复发布。
func CommitGroup(deliveries map[string]DeliveryState, groups map[string]PublishGroup, archives map[string]CandidateArchive, groupID string, unavailable map[string]bool, projectVersion string) (map[string]DeliveryState, PublishGroup, error) {
	group, exists := groups[groupID]
	if !exists {
		return deliveries, PublishGroup{}, fmt.Errorf("发布组 %s 不存在", groupID)
	}
	switch group.Status {
	case GroupStatusCommitted:
		return deliveries, group, nil // 幂等：重试与页面刷新不会重复发布
	case GroupStatusCancelled:
		return deliveries, group, fmt.Errorf("发布组 %s 已取消，不能提交", groupID)
	}
	if err := validateGroupMembers(deliveries, group, projectVersion); err != nil {
		return deliveries, group, err
	}
	if err := validateGroupDependencies(deliveries, group); err != nil {
		return deliveries, group, err
	}
	if err := checkGroupConflicts(deliveries, group); err != nil {
		return deliveries, group, err
	}
	group.Status = GroupStatusCommitting
	groups[groupID] = group
	for _, member := range group.Members {
		if unavailable[member.NodeID] {
			group.Note = fmt.Sprintf("接收方 %s 暂时不可用，整组停留在提交中：已发布 0 个接收方，恢复后继续", member.NodeID)
			groups[groupID] = group
			return deliveries, group, fmt.Errorf("接收方 %s 暂时不可用：整组保持提交中，未发布任何结果", member.NodeID)
		}
	}
	// 原子发布整组：读取者只能看到完整旧组或完整新组。
	adopted := map[string]GroupAdoption{}
	nodeIDs := make([]string, 0, len(group.Members))
	for _, member := range group.Members {
		nodeIDs = append(nodeIDs, member.NodeID)
	}
	sort.Strings(nodeIDs)
	members := memberMap(group)
	for _, nodeID := range nodeIDs {
		member := members[nodeID]
		state := deliveries[nodeID]
		published := *state.Pending
		state.Published = &published
		state.Pending = nil
		state.History = append(state.History, DeliveryEvent{Kind: EventPublished, Version: published.Version, RunID: published.RunID, Note: fmt.Sprintf("发布组 %s 一致提交，结果对接收方可见", groupID)})
		deliveries[nodeID] = state
		adopted[nodeID] = GroupAdoption{NodeID: nodeID, Version: member.Version, RunID: member.RunID}
		MarkArchivePublished(archives, published)
	}
	group.Adopted = adopted
	group.Status = GroupStatusCommitted
	group.Note = "整组已共同确认，所有接收方同时可见新版本"
	groups[groupID] = group
	return deliveries, group, nil
}

// RecoverGroup 把停留在提交中的组恢复到确定结局：
// 重新执行提交（幂等），已成功则 committed；候选或前提已失效时保持 committing 并说明原因。
// 已经进入提交的组不允许取消，必须走到确定结局。
func RecoverGroup(deliveries map[string]DeliveryState, groups map[string]PublishGroup, archives map[string]CandidateArchive, groupID string, projectVersion string) (map[string]DeliveryState, PublishGroup, error) {
	group, exists := groups[groupID]
	if !exists {
		return deliveries, PublishGroup{}, fmt.Errorf("发布组 %s 不存在", groupID)
	}
	if group.Status != GroupStatusCommitting {
		return deliveries, group, nil
	}
	return CommitGroup(deliveries, groups, archives, groupID, nil, projectVersion)
}

// CancelGroup 取消尚未进入提交阶段的发布组；候选本身保持待确认，可另行成组。
func CancelGroup(groups map[string]PublishGroup, groupID string) (PublishGroup, error) {
	group, exists := groups[groupID]
	if !exists {
		return PublishGroup{}, fmt.Errorf("发布组 %s 不存在", groupID)
	}
	switch group.Status {
	case GroupStatusPrepared:
		group.Status = GroupStatusCancelled
		group.Note = "用户在提交前取消整组，未发布任何结果"
		groups[groupID] = group
		return group, nil
	case GroupStatusCancelled:
		return group, nil
	default:
		return group, fmt.Errorf("发布组 %s 已进入提交阶段，不能取消，必须恢复到确定结局", groupID)
	}
}
