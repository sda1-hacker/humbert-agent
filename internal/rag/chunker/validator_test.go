package chunker

import (
	"strings"
	"testing"
)

// chunkOfLen 是测试辅助函数。
//
// 根据指定 rune 数生成一个 Chunk。
//
// 例如：
//
//	chunkOfLen(50)
//
// 得到：
//
//	Content = "aaaa..."
//	          共 50 rune
//
// 这样 Validator 测试不用反复手写长字符串。
func chunkOfLen(length int) Chunk {
	return Chunk{
		Content: strings.Repeat("a", length),
	}
}

// TestValidateChunksNoChunks
//
// Rule 1：
//
//	没有产生任何 Chunk
//
// 必须 Reject。
func TestValidateChunksNoChunks(t *testing.T) {
	result := ValidateChunks(nil, 1000, 512)

	if result.OK {
		t.Fatal("没有产生 Chunk 时不应该通过校验")
	}

	if result.Reason != validationReasonNoChunks {
		t.Fatalf("Reason 错误: want=%q got=%q", validationReasonNoChunks, result.Reason)
	}
}

// TestValidateChunksSingleChunkForLargeDocument
//
// Rule 2：
//
//	大文档只产生一个 Chunk
//
// 应该 Reject。
func TestValidateChunksSingleChunkForLargeDocument(t *testing.T) {
	chunks := []Chunk{
		chunkOfLen(500),
	}

	// ChunkSize = 500
	//
	// 2 * ChunkSize = 1000
	//
	// totalChars = 1001
	//
	// 满足：
	//
	//     totalChars > 2 * ChunkSize
	result := ValidateChunks(chunks, 1001, 500)

	if result.OK {
		t.Fatal("大文档只产生一个 Chunk 时应该被拒绝")
	}

	if result.Reason != validationReasonSingleLargeDocument {
		t.Fatalf(
			"Reason 错误: want=%q got=%q",
			validationReasonSingleLargeDocument,
			result.Reason,
		)
	}
}

// TestValidateChunksSingleChunkAtBoundary
//
// 固定 Rule 2 的边界行为。
//
// 条件是：
//
//	totalChars > 2 * chunkSize
//
// 而不是：
//
//	>=
//
// 所以刚好等于两倍时不应该因为 Rule 2 被拒绝。
func TestValidateChunksSingleChunkAtBoundary(t *testing.T) {
	chunks := []Chunk{
		chunkOfLen(500),
	}

	result := ValidateChunks(chunks, 1000, 500)

	if !result.OK {
		t.Fatalf("文档刚好等于 2x ChunkSize 时应该允许: reason=%q", result.Reason)
	}
}

// TestValidateChunksTooManyTinyChunks
//
// Rule 3：
//
//	非最后 Chunk 中 Tiny Chunk 太多
//
// 应该 Reject。
func TestValidateChunksTooManyTinyChunks(t *testing.T) {
	chunks := []Chunk{
		chunkOfLen(20),
		chunkOfLen(30),
		chunkOfLen(40),

		chunkOfLen(100),
		chunkOfLen(100),
		chunkOfLen(100),
		chunkOfLen(100),
		chunkOfLen(100),
	}

	// 一共 8 个 Chunk。
	//
	// 8 / 4 = 2
	//
	// Tiny：
	//
	//     20
	//     30
	//     40
	//
	// 共 3 个。
	//
	// 满足：
	//
	//     3 > 2
	//     3 > 2
	//
	// 所以 Reject。
	result := ValidateChunks(chunks, 590, 200)

	if result.OK {
		t.Fatal("Tiny Chunk 过多时应该被拒绝")
	}

	if result.Reason != validationReasonTooManyTinyChunks {
		t.Fatalf(
			"Reason 错误: want=%q got=%q",
			validationReasonTooManyTinyChunks,
			result.Reason,
		)
	}
}

// TestValidateChunksTwoTinyChunksAreAllowed
//
// 即使比例看起来较高，
//
//	tinyCount <= 2
//
// 当前 WeKnora 仍然会接受。
//
// 这是 Validator “刻意宽松”的体现。
func TestValidateChunksTwoTinyChunksAreAllowed(t *testing.T) {
	chunks := []Chunk{
		chunkOfLen(20),
		chunkOfLen(30),
		chunkOfLen(100),
		chunkOfLen(100),
		chunkOfLen(100),
	}

	result := ValidateChunks(chunks, 350, 200)

	if !result.OK {
		t.Fatalf("只有两个 Tiny Chunk 时当前规则应该允许: reason=%q", result.Reason)
	}
}

// TestValidateChunksTinyLastChunkIsAllowed
//
// 最后一块很小通常只是自然的尾部残留。
//
// Validator 明确跳过最后一个 Chunk 的 Tiny 检查。
func TestValidateChunksTinyLastChunkIsAllowed(t *testing.T) {
	chunks := []Chunk{
		chunkOfLen(100),
		chunkOfLen(100),
		chunkOfLen(10),
	}

	result := ValidateChunks(chunks, 210, 200)

	if !result.OK {
		t.Fatalf("最后一个 Tiny Chunk 应该允许: reason=%q", result.Reason)
	}
}

// TestValidateChunksAllFarBelowTarget
//
// Rule 4：
//
//	连最大的 Chunk 都不到目标大小 25%
//
// 并且：
//
//	原文总长度 > ChunkSize
//
// 应该认为分块过碎。
func TestValidateChunksAllFarBelowTarget(t *testing.T) {
	// ChunkSize = 400
	//
	// ChunkSize / 4 = 100
	//
	// 最大块只有 99。
	chunks := []Chunk{
		chunkOfLen(99),
		chunkOfLen(90),
		chunkOfLen(80),
		chunkOfLen(70),
		chunkOfLen(60),
	}

	result := ValidateChunks(chunks, 500, 400)

	if result.OK {
		t.Fatal("所有 Chunk 都远低于目标大小时应该被拒绝")
	}

	if result.Reason != validationReasonAllChunksTooSmall {
		t.Fatalf(
			"Reason 错误: want=%q got=%q",
			validationReasonAllChunksTooSmall,
			result.Reason,
		)
	}
}

// TestValidateChunksQuarterBoundaryIsAllowed
//
// Rule 4 是严格：
//
//	maxLen < chunkSize / 4
//
// 如果刚好等于 25%，不会 Reject。
func TestValidateChunksQuarterBoundaryIsAllowed(t *testing.T) {
	chunks := []Chunk{
		chunkOfLen(100),
		chunkOfLen(100),
		chunkOfLen(100),
		chunkOfLen(100),
		chunkOfLen(100),
	}

	result := ValidateChunks(chunks, 500, 400)

	if !result.OK {
		t.Fatalf("最大 Chunk 刚好达到目标25%%时应该允许: reason=%q", result.Reason)
	}
}

// TestValidateChunksShortDocumentCanBeBelowQuarter
//
// 即使 Chunk 很小，
//
// 只要：
//
//	totalChars <= chunkSize
//
// 就不能用 Rule 4 判定它过度碎片化。
//
// 因为原文自己本来就很短。
func TestValidateChunksShortDocumentCanBeBelowQuarter(t *testing.T) {
	chunks := []Chunk{
		chunkOfLen(80),
	}

	result := ValidateChunks(chunks, 80, 512)

	if !result.OK {
		t.Fatalf("短文档不应该因为低于目标尺寸而失败: reason=%q", result.Reason)
	}
}

// TestValidateChunksChunkExceeds2xTarget
//
// Rule 5：
//
//	maxLen > 2 * chunkSize
//
// Reject。
func TestValidateChunksChunkExceeds2xTarget(t *testing.T) {
	chunks := []Chunk{
		chunkOfLen(801),
		chunkOfLen(200),
	}

	result := ValidateChunks(chunks, 1001, 400)

	if result.OK {
		t.Fatal("Chunk 超过目标尺寸2倍时应该被拒绝")
	}

	if result.Reason != validationReasonChunkTooLarge {
		t.Fatalf(
			"Reason 错误: want=%q got=%q",
			validationReasonChunkTooLarge,
			result.Reason,
		)
	}
}

// TestValidateChunksExactly2xTargetIsAllowed
//
// Rule 5 使用：
//
//	>
//
// 而不是：
//
//	>=
//
// 所以：
//
//	maxLen == 2 * chunkSize
//
// 当前是允许的。
func TestValidateChunksExactly2xTargetIsAllowed(t *testing.T) {
	chunks := []Chunk{
		chunkOfLen(800),
		chunkOfLen(200),
	}

	result := ValidateChunks(chunks, 1000, 400)

	if !result.OK {
		t.Fatalf("Chunk 刚好等于目标尺寸2倍时应该允许: reason=%q", result.Reason)
	}
}

// TestValidateChunksNormalResult
//
// 一个正常分块结果应该通过。
func TestValidateChunksNormalResult(t *testing.T) {
	chunks := []Chunk{
		chunkOfLen(450),
		chunkOfLen(500),
		chunkOfLen(480),
		chunkOfLen(200),
	}

	result := ValidateChunks(chunks, 1630, 512)

	if !result.OK {
		t.Fatalf("正常 Chunk 结果应该通过: reason=%q", result.Reason)
	}

	if result.Reason != "" {
		t.Fatalf("成功结果 Reason 应为空: got=%q", result.Reason)
	}
}

// TestValidateChunksUsesRuneLength
//
// 再固定一次整个 Chunker 的核心约定：
//
//	长度按 rune
//
// 而不是 UTF-8 byte。
func TestValidateChunksUsesRuneLength(t *testing.T) {
	chunks := []Chunk{
		{Content: strings.Repeat("中", 100)},
		{Content: strings.Repeat("国", 100)},
	}

	// 每块是 100 rune。
	//
	// 如果错误使用 len(string)，
	// 中文 UTF-8 会变成 300 byte，
	// 测试行为就会改变。
	result := ValidateChunks(chunks, 200, 400)

	if !result.OK {
		t.Fatalf("Validator 必须使用 rune 长度: reason=%q", result.Reason)
	}
}
