package services

import (
	"errors"
	"fmt"
	"github.com/sda1-hacker/humbert-agent/internal/computer"
)

// 桌面控制不能操作宿主自身的授权/配置；先停止桌面任务，用户再通过真实 UI 修改。
func humanUIWrite() error {
	if computer.InputActive() {
		return errors.New("桌面自动化正在输入，请先停止桌面任务，再由用户修改设置或处理审批")
	}
	return nil
}

// dtoResult 统一简单 Bridge 转换的错误出口，不改变调用者的 Context 与副作用顺序。
func dtoResult[A, B any](message string, convert func(A) B) func(A, error) (B, error) {
	return func(value A, err error) (B, error) {
		if err != nil {
			var zero B
			return zero, fmt.Errorf(message, err)
		}
		return convert(value), nil
	}
}

func mapDTO[A, B any](values []A, convert func(A) B) []B {
	result := make([]B, len(values))
	for i, value := range values {
		result[i] = convert(value)
	}
	return result
}
