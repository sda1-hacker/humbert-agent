package skills

import "errors"

var (
	// ErrSkillNotFound 表示请求的 Skill 不存在。调用方可以通过 errors.Is 判断是否需要
	// 刷新 UI 或修复 Agent Profile，而不依赖错误字符串。
	ErrSkillNotFound = errors.New("skill 不存在")

	// ErrSkillExists 表示普通“安装”目标已经存在。Install 永远不会静默覆盖；已经安装的 Skill
	// 必须通过显式 Update/Reinstall 流程原子替换，以保留用户对覆盖行为的控制。
	ErrSkillExists = errors.New("skill 已存在")

	// ErrInvalidSkill 表示 SKILL.md、目录结构或包内文件违反 Humbert 的 Skill 约束。
	ErrInvalidSkill = errors.New("skill 包无效")

	// ErrUnsupportedSkill 表示 Skill Package 本身有效且可以安装/查看，但声明了当前
	// Humbert + Eino Runtime 尚未开放的执行能力。安装有效性与运行兼容性严格分离；
	// 控制面可以保留这类 Skill，但 Agent.enabled_skills 不能新增该引用。
	ErrUnsupportedSkill = errors.New("skill 使用了当前不支持的能力")

	// ErrSkillSnapshotStale 表示当前 Turn 冻结的 Skill Snapshot 与磁盘文件已经不同。
	// Runtime 不会在同一个 Turn 中悄悄切换到新内容，而是要求下一 Turn 重新 Resolve。
	ErrSkillSnapshotStale = errors.New("skill snapshot 已过期")

	// ErrSkillAssetNotFound 表示 Snapshot 中不存在请求的相对资源路径。
	ErrSkillAssetNotFound = errors.New("skill 资源不存在")

	// ErrSkillAssetBinary 表示模型请求读取的 Skill 附件不是 UTF-8 文本。当前 Skill Tool
	// 只返回文本资源，图片/二进制文件以后应通过专门的多模态能力处理。
	ErrSkillAssetBinary = errors.New("skill 资源不是文本文件")

	// ErrSkillSourceUnknown 表示当前 Skill 没有 Humbert 可复用的安装来源。常见于升级到
	// Source Metadata 功能之前安装的包或用户手工复制的目录；Skill 仍可正常使用，但自动
	// Check Update / Reinstall 需要先通过详情页重新选择 URL 或本地目录建立来源。
	ErrSkillSourceUnknown = errors.New("skill 安装来源未知")
)
