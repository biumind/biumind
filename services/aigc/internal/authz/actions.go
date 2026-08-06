package authz

import (
	"context"

	"github.com/google/uuid"
)

// ─── 常用 action 命名空间 ─────────────────────────────
//
// 命名约定: aigc:<resource>:<verb>
//
// 注意: 这些只是字符串常量, Cedar policy 文件里 (services/authz/internal/policies/)
// 才是真实的允许/拒绝规则. 这里只列出 aigc 服务**会用到**的 action 名,
// 让 endpoint 代码用常量而非裸字符串.

const (
	// Tasks
	ActionCreateTask        = "aigc:tasks:create"
	ActionReadTask          = "aigc:tasks:read"
	ActionListMyTasks       = "aigc:tasks:list_mine"
	ActionUpdateTaskVis     = "aigc:tasks:update_visibility"
	ActionDeleteTask        = "aigc:tasks:delete"
	ActionCancelTask        = "aigc:tasks:cancel"

	// Models / Providers
	ActionListModels        = "aigc:models:list"
	ActionListProviders     = "aigc:providers:list" // admin only

	// Gallery (公开作品瀑布流; 不需登录就能看)
	ActionReadGallery       = "aigc:gallery:read"

	// Characters
	ActionCreateCharacter   = "aigc:characters:create"
	ActionListCharacters    = "aigc:characters:list"
	ActionDeleteCharacter   = "aigc:characters:delete"

	// Hotparse (P5)
	ActionUploadHotparse    = "aigc:hotparse:upload"
	ActionAnalyzeHotparse   = "aigc:hotparse:analyze"
	ActionParseHotparseURL  = "aigc:hotparse:parse_url"

	// Prompt 优化 (流式)
	ActionOptimizePrompt    = "aigc:prompt:optimize"
)

// ─── Cedar entity types ───────────────────────────────

const (
	EntityUser      = "User"
	EntityTask      = "aigc.Task"
	EntityModel     = "aigc.Model"
	EntityProvider  = "aigc.Provider"
	EntityGallery   = "aigc.Gallery"
	EntityCharacter = "aigc.Character"
	EntityHotparse  = "aigc.HotparseVideo"
)

// ─── Helper: PrincipalUser ────────────────────────────

// PrincipalUser 构造 User 类型 principal entity. plan / role 作为 attributes
// 让 Cedar policy 可以按 plan 区分 (free / pro / team).
func PrincipalUser(userID uuid.UUID, plan, role string) Entity {
	attrs := map[string]any{}
	if plan != "" {
		attrs["plan"] = plan
	}
	if role != "" {
		attrs["role"] = role
	}
	return Entity{Type: EntityUser, ID: userID.String(), Attributes: attrs}
}

// ResourceTask 构造 Task entity (含 owner / is_public 让 policy 决策 "owner-only"
// vs "public read" 这类规则).
func ResourceTask(taskID, ownerID uuid.UUID, isPublic bool) Entity {
	return Entity{
		Type: EntityTask,
		ID:   taskID.String(),
		Attributes: map[string]any{
			"owner_id":  ownerID.String(),
			"is_public": isPublic,
		},
	}
}

// ResourceCharacter 构造 Character entity.
func ResourceCharacter(charID uuid.UUID, ownerID *uuid.UUID, isPublic bool) Entity {
	attrs := map[string]any{"is_public": isPublic}
	if ownerID != nil {
		attrs["owner_id"] = ownerID.String()
	}
	return Entity{Type: EntityCharacter, ID: charID.String(), Attributes: attrs}
}

// ResourceGallery / ResourceModelByCode — 简单 entity (无 attributes).
func ResourceGallery() Entity         { return Entity{Type: EntityGallery, ID: "global"} }
func ResourceModelByCode(code string) Entity {
	return Entity{Type: EntityModel, ID: code}
}

// ─── Allow 助手 ───────────────────────────────────────

// Authorize 直接返回 (allowed bool, error). 调用方写法:
//
//	ok, err := authz.Authorize(ctx, dec, principal, action, resource)
//	if err != nil { /* fail-closed → 403 */ }
//	if !ok { return 403 }
func Authorize(ctx context.Context, d Decider, principal Entity, action string, resource Entity) (bool, error) {
	r, err := d.Check(ctx, Request{Principal: principal, Action: action, Resource: resource})
	if err != nil {
		return false, err // 调用方 fail-closed
	}
	return r.Allowed(), nil
}
