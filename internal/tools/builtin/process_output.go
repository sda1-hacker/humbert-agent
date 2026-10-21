package builtin

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
)

// boundedCommandOutput 对 stdout/stderr 做固定内存预算。
//
// 为了同时保留命令开头上下文和结尾错误信息，超过预算以后保留 head + tail，
// 中间被丢弃。Write 永远返回 len(p)，因此不会因为 UI 输出达到上限而向子进程
// 制造 Broken Pipe。
type boundedCommandOutput struct {
	mu        sync.Mutex
	limit     int
	headLimit int
	tailLimit int
	head      bytes.Buffer
	tail      []byte
	total     int64
}

func newBoundedCommandOutput(limit int) *boundedCommandOutput {
	headLimit := limit / 2
	if headLimit == 0 {
		headLimit = limit
	}
	return &boundedCommandOutput{limit: limit, headLimit: headLimit, tailLimit: limit - headLimit}
}

func (b *boundedCommandOutput) Write(p []byte) (int, error) {
	originalLength := len(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(originalLength)
	remainingHead := b.headLimit - b.head.Len()
	if remainingHead > 0 {
		take := remainingHead

		if len(p) < take {
			take = len(p)
		}

		_, _ = b.head.Write(p[:take])

		p = p[take:]
	}
	if len(p) > 0 && b.tailLimit > 0 {
		b.tail = append(b.tail, p...)

		if len(b.tail) > b.tailLimit {
			overflow := len(b.tail) - b.tailLimit

			copy(b.tail, b.tail[overflow:])

			b.tail = b.tail[:b.tailLimit]
		}
	}
	return originalLength, nil
}

func (b *boundedCommandOutput) Result() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total <= int64(b.limit) {
		combined := append([]byte(nil), b.head.Bytes()...)

		combined = append(combined, b.tail...)

		return strings.ToValidUTF8(string(combined), "�"), false
	}
	dropped := b.total - int64(b.head.Len()) - int64(len(b.tail))
	var builder strings.Builder
	builder.Write(b.head.Bytes())
	fmt.Fprintf(&builder, "\n... [truncated %d bytes] ...\n", dropped)
	builder.Write(b.tail)
	return strings.ToValidUTF8(builder.String(), "�"), true
}
