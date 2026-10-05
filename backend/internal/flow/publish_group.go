package flow

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// 发布组状态：准备（可取消）→ 提交中（只能恢复到确定结局）→ 已完成；
// 校验或冲突失败进入已中止（整组未发布），准备阶段可取消。
const (
	GroupPrepared   = "prepared"
	GroupCommitting = "committing"
	GroupCommitted  = "committed"
	GroupAborted    = "aborted"
	GroupCancelled  = "cancelled"
)

// PublishGroupMember 绑定组内一个接收方要发布的候选身份（版本 + 运行批次）
// 以及建组时该接收方对外可见的基准版本（冲突判定的共同前提）。
type PublishGroupMember struct {
	NodeID      string `json:"nodeId"`
	Receiver    string `json:"receiver"`
	Version     string `json:"version"`
	RunID       string `json:"runId"`
	BaseVersion string `json:"baseVersion"`
}

// PublishGroupDependency 表示接收方之间的版本依赖：
// Receiver 的发布以 OnReceiver 在组发布后处于 Version 为前提。
type PublishGroupDependency struct {
	Receiver   string `json:"receiver"`
	OnReceiver string `json:"onReceiver"`
	Version    string `json:"version"`
}

// ReceiverOutcome 记录提交阶段每个接收方实际采用的候选身份：
// 重试与恢复据此跳过已完成接收方，不会重复发布。
type ReceiverOutcome struct {
	Receiver  string `json:"receiver"`
	NodeID    string `json:"nodeId"`
	Version   string `json:"version"`
	RunID     string `json:"runId"`
	Committed bool   `json:"committed"`
	Note      string `json:"note,omitempty"`
}

// PublishGroup 是一次多接收方候选的一致发布事务。
type PublishGroup struct {
	ID             string                   `json:"id"`
	RunID          string                   `json:"runId"`
	ProjectVersion string                   `json:"projectVersion"`
	Members        []PublishGroupMember     `json:"members"`
	Dependencies   []PublishGroupDependency `json:"dependencies"`
	Status         string                   `json:"status"`
	StatusNote     string                   `json:"statusNote,omitempty"`
	Outcomes       []ReceiverOutcome        `json:"outcomes,omitempty"`
	CreatedAt      time.Time                `json:"createdAt"`
	UpdatedAt      time.Time                `json:"updatedAt"`
}

func memberOrder(group *PublishGroup) []PublishGroupMember {
	deps := map[string][]string{}
	for _, dep := range group.Dependencies {
		deps[dep.Receiver] = append(deps[dep.Receiver], dep.OnReceiver)
	}
	ordered := make([]PublishGroupMember, 0, len(group.Members))
	visited := map[string]bool{}
	var visit func(receiver string)
	byReceiver := map[string]PublishGroupMember{}
	for _, member := range group.Members {
		byReceiver[member.Receiver] = member
	}
	visit = func(receiver string) {
		if visited[receiver] {
			return
		}
		visited[receiver] = true
		upstream := append([]string{}, deps[receiver]...)
		sort.Strings(upstream)
		for _, on := range upstream {
			if _, ok := byReceiver[on]; ok {
				visit(on)
			}
		}
		if member, ok := byReceiver[receiver]; ok {
			ordered = append(ordered, member)
		}
	}
	receivers := make([]string, 0, len(byReceiver))
	for receiver := range byReceiver {
		receivers = append(receivers, receiver)
	}
	sort.Strings(receivers)
	for _, receiver := range receivers {
		visit(receiver)
	}
	return ordered
}

// validateDependencyAcyclic 检查组内接收方依赖无环（如 A→B→C→A 必须拒绝）。
func validateDependencyAcyclic(group *PublishGroup) error {
	deps := map[string][]string{}
	memberReceivers := map[string]bool{}
	for _, member := range group.Members {
		memberReceivers[member.Receiver] = true
	}
	for _, dep := range group.Dependencies {
		deps[dep.Receiver] = append(deps[dep.Receiver], dep.OnReceiver)
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(receiver string) error
	visit = func(receiver string) error {
		if color[receiver] == gray {
			return fmt.Errorf("接收方 %s 的依赖形成循环", receiver)
		}
		if color[receiver] == black {
			return nil
		}
		color[receiver] = gray
		for _, on := range deps[receiver] {
			if memberReceivers[on] {
				if err := visit(on); err != nil {
					return err
				}
			}
		}
		color[receiver] = black
		return nil
	}
	for receiver := range memberReceivers {
		if err := visit(receiver); err != nil {
			return err
		}
	}
	return nil
}

// CreatePublishGroup 把一次运行中的若干待确认候选组成发布组。
// 建组即共同校验：候选齐全且未失效、依赖版本一致、依赖无环；
// 每个成员记录接收方当前可见版本作为冲突判定的共同前提。
func CreatePublishGroup(deliveries map[string]DeliveryState, nodeIDs []string, dependencies []PublishGroupDependency, groupID string, runID string, projectVersion string) (*PublishGroup, error) {
	if len(nodeIDs) < 2 {
		return nil, errors.New("发布组至少需要两个接收方候选")
	}
	members := make([]PublishGroupMember, 0, len(nodeIDs))
	seenReceiver := map[string]bool{}
	for _, nodeID := range nodeIDs {
		state, exists := deliveries[nodeID]
		if !exists || state.Pending == nil {
			return nil, fmt.Errorf("节点 %s 没有待确认候选，发布组无法组成", nodeID)
		}
		if state.Pending.Stale {
			return nil, fmt.Errorf("节点 %s 的候选已失效（%s），不能加入发布组", nodeID, state.Pending.StaleReason)
		}
		receiver := state.Receiver
		if seenReceiver[receiver] {
			return nil, fmt.Errorf("接收方 %s 在组内重复，一个接收方只能有一个候选", receiver)
		}
		seenReceiver[receiver] = true
		base := ""
		if state.Published != nil {
			base = state.Published.Version
		}
		members = append(members, PublishGroupMember{NodeID: nodeID, Receiver: receiver, Version: state.Pending.Version, RunID: state.Pending.RunID, BaseVersion: base})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Receiver < members[j].Receiver })
	group := &PublishGroup{
		ID:             groupID,
		RunID:          runID,
		ProjectVersion: projectVersion,
		Members:        members,
		Dependencies:   append([]PublishGroupDependency{}, dependencies...),
		Status:         GroupPrepared,
		CreatedAt:      time.Now().UTC(),
	}
	group.UpdatedAt = group.CreatedAt
	if err := validateDependencyAcyclic(group); err != nil {
		return nil, err
	}
	if err := checkDependencyVersions(group, deliveries); err != nil {
		return nil, err
	}
	return group, nil
}

// checkDependencyVersions 校验依赖版本一致：被依赖接收方在组发布后的版本
// （组内成员取其候选版本，组外接收方取其当前可见版本）必须等于依赖声明的版本。
func checkDependencyVersions(group *PublishGroup, deliveries map[string]DeliveryState) error {
	resultVersion := map[string]string{}
	for _, member := range group.Members {
		resultVersion[member.Receiver] = member.Version
	}
	for _, dep := range group.Dependencies {
		version, inGroup := resultVersion[dep.OnReceiver]
		if !inGroup {
			found := false
			for _, state := range deliveries {
				if state.Receiver == dep.OnReceiver && state.Published != nil {
					version = state.Published.Version
					found = true
				}
			}
			if !found {
				return fmt.Errorf("接收方 %s 依赖的接收方 %s 尚未有任何可见版本", dep.Receiver, dep.OnReceiver)
			}
		}
		if version != dep.Version {
			return fmt.Errorf("接收方 %s 依赖 %s 的版本 %s，但实际将是版本 %s，依赖版本不一致", dep.Receiver, dep.OnReceiver, dep.Version, version)
		}
	}
	return nil
}

// ConfirmPublishGroup 驱动发布组进入并完成提交；对 committing 组调用即恢复。
// 进入提交前共同校验：候选齐全且依据仍有效、接收方可见版本仍是建组时的共同前提、
// 依赖版本一致；任一不满足整组中止，不发布任何接收方。
// 提交按依赖顺序逐接收方落账并记录实际采用的候选身份；接收方暂时不可用时
// 组停留在提交中，已完成的接收方不会重试重复发布。
// unavailable 模拟接收方暂时不可用（页面可切换，用于验证断开与恢复）。
func ConfirmPublishGroup(deliveries map[string]DeliveryState, archives map[string]CandidateArchive, group *PublishGroup, unavailable map[string]bool, projectVersion string, eventRunID string) (map[string]DeliveryState, error) {
	switch group.Status {
	case GroupCommitted:
		return deliveries, nil // 幂等：重复确认 / 页面刷新不重复发布
	case GroupCancelled:
		return deliveries, errors.New("发布组已取消，不能确认")
	case GroupAborted:
		return deliveries, errors.New("发布组已中止（" + group.StatusNote + "），不能确认")
	}
	if group.Status == GroupPrepared {
		if err := precommitCheck(deliveries, group, projectVersion); err != nil {
			group.Status = GroupAborted
			group.StatusNote = err.Error()
			group.UpdatedAt = time.Now().UTC()
			return deliveries, err
		}
		group.Status = GroupCommitting
		group.UpdatedAt = time.Now().UTC()
	}
	committed := map[string]bool{}
	for _, outcome := range group.Outcomes {
		if outcome.Committed {
			committed[outcome.Receiver] = true
		}
	}
	for _, member := range memberOrder(group) {
		if committed[member.Receiver] {
			continue // 恢复 / 重试：该接收方已采用候选，跳过不重复发布
		}
		if unavailable[member.Receiver] {
			group.StatusNote = fmt.Sprintf("接收方 %s 暂时不可用，组停留在提交中；恢复后重试即可继续", member.Receiver)
			group.UpdatedAt = time.Now().UTC()
			return deliveries, fmt.Errorf("接收方 %s 暂时不可用，发布组仍在提交中", member.Receiver)
		}
		state := deliveries[member.NodeID]
		// 恢复途中候选被取代属于异常：已进入提交的组必须能落到确定结局，
		// 按组内登记的候选身份发布，保证整组一致。
		published := Delivery{
			NodeID:         member.NodeID,
			Receiver:       member.Receiver,
			Version:        member.Version,
			RunID:          member.RunID,
			ProjectVersion: group.ProjectVersion,
		}
		if state.Pending != nil && state.Pending.Version == member.Version && state.Pending.RunID == member.RunID {
			published = *state.Pending
		}
		state.Published = &published
		if state.Pending != nil && state.Pending.Version == member.Version && state.Pending.RunID == member.RunID {
			state.Pending = nil
		}
		state.History = append(state.History, DeliveryEvent{Kind: EventPublished, Version: published.Version, RunID: eventRunID, Note: "发布组 " + group.ID + " 一致提交，结果对接收方可见"})
		deliveries[member.NodeID] = state
		MarkArchivePublished(archives, published)
		group.Outcomes = append(group.Outcomes, ReceiverOutcome{Receiver: member.Receiver, NodeID: member.NodeID, Version: member.Version, RunID: member.RunID, Committed: true, Note: "接收方实际采用的候选"})
		group.UpdatedAt = time.Now().UTC()
	}
	group.Status = GroupCommitted
	group.StatusNote = "全部接收方已采用组内候选，整组对接收方可见"
	group.UpdatedAt = time.Now().UTC()
	return deliveries, nil
}

// precommitCheck 是 prepared → committing 的共同校验：
// 输入依据仍有效（候选未失效、流程修订未变）、必需候选齐全、
// 接收方当前可见版本仍是建组时的共同前提（冲突判定）、依赖版本一致。
// 严格按版本身份判定：候选输出内容相同不能绕过依据变化或覆盖别组的新版本。
func precommitCheck(deliveries map[string]DeliveryState, group *PublishGroup, projectVersion string) error {
	if projectVersion != "" && group.ProjectVersion != "" && group.ProjectVersion != projectVersion {
		return errors.New("建组后工程已被修改，组失去流程修订依据，整组保持未发布")
	}
	for _, member := range group.Members {
		state, exists := deliveries[member.NodeID]
		if !exists || state.Pending == nil {
			return fmt.Errorf("接收方 %s 的候选已不存在（可能被新运行取代），整组保持未发布", member.Receiver)
		}
		if state.Pending.Version != member.Version || state.Pending.RunID != member.RunID {
			return fmt.Errorf("接收方 %s 的候选已更新（建组时为 %s/%s），整组保持未发布", member.Receiver, member.Version, member.RunID)
		}
		if reason := rejectReason(state.Pending, projectVersion); reason != "" {
			return fmt.Errorf("接收方 %s 的候选已失效：%s，整组保持未发布", member.Receiver, reason)
		}
		visible := ""
		if state.Published != nil {
			visible = state.Published.Version
		}
		// 冲突判定以接收方当前可见版本为共同前提：即使候选输出与当前可见
		// 版本内容相同，也不绕过基准检查、不覆盖别组已发布的新版本。
		if visible != member.BaseVersion {
			return fmt.Errorf("接收方 %s 的可见版本已变为 %s（建组前提为 %s），与其它发布组冲突，整组保持未发布", member.Receiver, visible, member.BaseVersion)
		}
	}
	return checkDependencyVersions(group, deliveries)
}

// CancelPublishGroup 取消尚未进入提交阶段的发布组；已进入提交的组必须恢复
// 到确定结局，不能取消。
func CancelPublishGroup(group *PublishGroup) error {
	switch group.Status {
	case GroupPrepared:
		group.Status = GroupCancelled
		group.StatusNote = "用户在提交前取消，整组未发布"
		group.UpdatedAt = time.Now().UTC()
		return nil
	case GroupCommitting:
		return errors.New("发布组已进入提交阶段，不能取消；请恢复到确定结局")
	case GroupCommitted:
		return errors.New("发布组已完成，不能取消")
	default:
		return errors.New("发布组已结束（" + group.Status + "），不能取消")
	}
}
