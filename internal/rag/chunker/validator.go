package chunker

// Validator 中使用的一些固定阈值。
//
// 这些值当前与 WeKnora 的行为保持一致。
const (
	// tinyChunkThreshold 定义非尾块的过小阈值，严格小于该值才计为碎片。
	tinyChunkThreshold = 50

	// maxChunkTargetMultiplier 表示：
	//
	// 如果某个 Chunk 超过：
	//
	//     ChunkSize * 2
	//
	// 就认为 Splitter 基本忽略了尺寸预算。
	maxChunkTargetMultiplier = 2

	// minUsefulTargetDivisor 用于判断最大块是否仍远小于目标大小。
	minUsefulTargetDivisor = 4
)

// ValidationResult 保存分块质量是否合格及拒绝原因。
type ValidationResult struct {
	OK     bool
	Reason string
}

// 拒绝原因保持稳定，供诊断输出与调用方判断使用。
const (
	validationReasonNoChunks            = "no chunks produced"
	validationReasonSingleLargeDocument = "single chunk for large document"
	validationReasonTooManyTinyChunks   = "too many tiny chunks"
	validationReasonAllChunksTooSmall   = "all chunks far below target size"
	validationReasonChunkTooLarge       = "chunk exceeds 2x target size"
)

// ValidateChunks 检查空结果、未充分切分、过多小块和严重超长块，供策略回退使用。
func ValidateChunks(chunks []Chunk, totalChars, chunkSize int) ValidationResult {
	// 空结果表示策略未产生有效分块。

	if len(chunks) == 0 {
		return ValidationResult{Reason: validationReasonNoChunks}
	}

	// 大于两倍目标大小的文档不能只产生一个块。

	if len(chunks) == 1 && totalChars > 2*chunkSize {
		return ValidationResult{Reason: validationReasonSingleLargeDocument}
	}

	// 统计最大块与非尾部小块数量，用于下面的质量判断。

	maxLen := 0
	tinyCount := 0

	for i, chunk := range chunks {
		length := RuneLen(chunk.Content)

		if length > maxLen {
			maxLen = length
		}

		// 尾块天然可能很小，不计入过多碎片判断。

		if i != len(chunks)-1 && length < tinyChunkThreshold {
			tinyCount++
		}
	}

	// 过小块须同时超过总块数的四分之一及两个，才判为系统性碎片化。

	if tinyCount > len(chunks)/4 && tinyCount > 2 {
		return ValidationResult{Reason: validationReasonTooManyTinyChunks}
	}

	// 文档比目标大小更长，但最大块仍不到目标的四分之一时判为过度切分。

	if maxLen < chunkSize/minUsefulTargetDivisor && totalChars > chunkSize {
		return ValidationResult{Reason: validationReasonAllChunksTooSmall}
	}

	// 某块严格超过目标大小两倍时判为超长，触发策略回退。

	if chunkSize > 0 && maxLen > maxChunkTargetMultiplier*chunkSize {
		return ValidationResult{Reason: validationReasonChunkTooLarge}
	}

	// -------------------------------------------------------------------------
	// 没有触发任何明显异常规则。
	//
	// 当前结果可以接受。
	// -------------------------------------------------------------------------

	return ValidationResult{OK: true}
}
