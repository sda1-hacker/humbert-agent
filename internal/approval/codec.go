package approval

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	// Eino v0.9.x 的 ADK Checkpoint 使用 encoding/gob。Model/Provider 运行态里常见的
	// 动态 JSON 值会以 map[string]any / []any 的具体类型挂在 interface 字段下面；
	// gob 对这类 interface concrete value 要求显式注册，否则 Tool Approval 保存 checkpoint
	// 会报 “type not registered for interface”。
	//
	// 这里用标准 gob 名称做兼容注册，并容忍未来 Eino 版本已经提前注册同一类型的情况。
	// 只注册 JSON 容器，不注册 Humbert 业务对象，避免扩大 checkpoint 的隐式协议面。
	registerCheckpointGobType(map[string]any{})
	registerCheckpointGobType([]any{})
}

func registerCheckpointGobType(value any) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message := fmt.Sprint(recovered)
			if strings.Contains(message, "gob: registering duplicate") {
				return
			}
			panic(recovered)
		}
	}()
	gob.Register(value)
}

// EncodeInterrupt 把用户可见审批信息与内部原始参数分别编码成 JSON 字符串。
//
// 两个字符串都会进入 Eino checkpoint，但只有 infoJSON 会通过 InterruptContexts.Info
// 暴露给 Runtime/Event/UI。stateJSON 只在被中断 Tool 恢复时读取。
func EncodeInterrupt(info InterruptInfo, arguments string) (infoJSON string, stateJSON string, err error) {
	if err := info.Validate(); err != nil {
		return "", "", err
	}
	infoBytes, err := json.Marshal(info)
	if err != nil {
		return "", "", fmt.Errorf("编码 Approval InterruptInfo 失败: %w", err)
	}
	stateBytes, err := json.Marshal(InterruptState{InfoJSON: string(infoBytes), Arguments: arguments})
	if err != nil {
		return "", "", fmt.Errorf("编码 Approval InterruptState 失败: %w", err)
	}
	return string(infoBytes), string(stateBytes), nil
}

// DecodeInterruptInfo 解析 Eino InterruptContexts.Info。Runtime 不接受未知/畸形 payload，
// 防止其他中断机制被错误当成 Tool Approval 恢复。
func DecodeInterruptInfo(value any) (InterruptInfo, error) {
	raw, ok := value.(string)
	if !ok {
		return InterruptInfo{}, fmt.Errorf("Approval InterruptInfo 类型无效: %T", value)
	}
	var info InterruptInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return InterruptInfo{}, fmt.Errorf("解析 Approval InterruptInfo 失败: %w", err)
	}
	if err := info.Validate(); err != nil {
		return InterruptInfo{}, err
	}
	return info, nil
}

// DecodeInterruptState 解析 Tool 恢复所需的 checkpoint 内部状态。
func DecodeInterruptState(raw string) (InterruptState, error) {
	var state InterruptState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return InterruptState{}, fmt.Errorf("解析 Approval InterruptState 失败: %w", err)
	}
	if state.InfoJSON == "" {
		return InterruptState{}, fmt.Errorf("Approval InterruptState 缺少 info")
	}
	return state, nil
}

// EncodeResumeData 把批准/拒绝结果编码成可由 Tool ResumeContext 读取的 JSON 字符串。
func EncodeResumeData(approved bool) (string, error) {
	data, err := json.Marshal(ResumeData{Approved: approved})
	if err != nil {
		return "", fmt.Errorf("编码 Approval ResumeData 失败: %w", err)
	}
	return string(data), nil
}

// DecodeResumeData 解析 Runtime 定向传入的审批结果。
func DecodeResumeData(raw string) (ResumeData, error) {
	var data ResumeData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return ResumeData{}, fmt.Errorf("解析 Approval ResumeData 失败: %w", err)
	}
	return data, nil
}
