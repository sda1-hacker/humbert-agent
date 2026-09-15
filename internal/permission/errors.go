package permission

import "errors"

var (
	// ErrDenied 表示 Permission Policy 明确拒绝了一次 Capability 调用。
	ErrDenied = errors.New("Capability 权限被拒绝")

	// ErrRuleNotFound 表示设置页尝试删除不存在的持久化规则。
	ErrRuleNotFound = errors.New("Permission Rule 不存在")

	// ErrInvalidApprovalScope 表示前端提交了当前版本不支持的授权范围。
	ErrInvalidApprovalScope = errors.New("Approval 授权范围不合法")
)
