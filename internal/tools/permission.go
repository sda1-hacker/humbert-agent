package tools

import "github.com/sda1-hacker/humbert-agent/internal/permission"

// Authorizer 是 Tool Registry 依赖的 Permission Domain 接口别名。
//
// 保留 tools.Authorizer 名称是为了让 Registry 的职责表达清晰，但真实策略、规则和审批动作
// 全部由 internal/permission 拥有。Builtin/Skill/MCP/Plugin Tool 因此可以共享同一授权边界。
type Authorizer = permission.Authorizer
