package services

import "fmt"

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
