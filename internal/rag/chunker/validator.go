package chunker

// Validator 中使用的一些固定阈值。
//
// 这些值当前与 WeKnora 的行为保持一致。
const (
	// tinyChunkThreshold 表示：
	//
	// 一个非尾部 Chunk 小于多少 rune，
	// 可以认为它是“明显过小的碎片”。
	//
	// 当前：
	//
	//     < 50 rune
	//
	// 注意是严格小于。
	//
	// 49 属于 Tiny Chunk，
	// 50 不属于。
	tinyChunkThreshold = 50

	// maxChunkTargetMultiplier 表示：
	//
	// 如果某个 Chunk 超过：
	//
	//     ChunkSize * 2
	//
	// 就认为 Splitter 基本忽略了尺寸预算。
	maxChunkTargetMultiplier = 2

	// minUsefulTargetDivisor 用于判断：
	//
	// 所有 Chunk 是否都远远小于目标尺寸。
	//
	// 当前规则：
	//
	//     maxChunkLen < ChunkSize / 4
	//
	// 也就是连目标尺寸 25% 都没达到。
	minUsefulTargetDivisor = 4
)

// ValidationResult 表示一次 Chunk 校验结果。
//
// OK:
//
//	true
//	    当前结果可以接受。
//
//	false
//	    当前结果明显异常，
//	    Strategy Resolver 应继续尝试下一个 Tier。
//
// Reason:
//
//	当 OK == false 时，记录具体拒绝原因。
//
// 后面 SplitWithDiagnostics() 会把这个 Reason
// 原样记录到：
//
//	Diagnostics.Rejected
//
// 中，因此这些 Reason 不只是日志信息，
// 也是调试接口的一部分。
type ValidationResult struct {
	OK     bool
	Reason string
}

// 当前拒绝原因与 WeKnora 保持相同文本。
//
// 为什么定义成常量，而不是直接散落在函数中？
//
// 因为后面：
//
//	Diagnostics
//	Preview UI
//	单元测试
//
// 都可能依赖这些稳定值。
const (
	validationReasonNoChunks            = "no chunks produced"
	validationReasonSingleLargeDocument = "single chunk for large document"
	validationReasonTooManyTinyChunks   = "too many tiny chunks"
	validationReasonAllChunksTooSmall   = "all chunks far below target size"
	validationReasonChunkTooLarge       = "chunk exceeds 2x target size"
)

// ValidateChunks 判断一个 Splitter 产生的 []Chunk
// 是否“足够合理，可以继续使用”。
//
// 参数：
//
//	chunks
//	    某一个分块策略产生的结果。
//
//	totalChars
//	    原始文档总 rune 数。
//
//	chunkSize
//	    当前目标 ChunkSize。
//
// 返回：
//
//	ValidationResult{OK: true}
//
// 或：
//
//	ValidationResult{
//	    OK: false,
//	    Reason: "...",
//	}
//
// -----------------------------------------------------------------------------
// 非常重要：
//
// Validator 并不试图评价：
//
//	“这个分块是不是最佳分块？”
//
// 它只判断：
//
//	“有没有明显坏掉？”
//
// 这是一个非常关键的设计。
//
// 如果 Validator 太严格，可能出现：
//
//	Heading
//	  ↓
//	看起来正常
//	  ↓
//	Validator Reject
//	  ↓
//	Heuristic
//	  ↓
//	Validator Reject
//	  ↓
//	Legacy
//
// 最终大量文档都被迫退回 Legacy，
// Auto Strategy 就失去了意义。
//
// 所以这里应该保持 permissive：
//
//	合理的差异允许存在，
//	明显异常才 Reject。
func ValidateChunks(chunks []Chunk, totalChars, chunkSize int) ValidationResult {
	// -------------------------------------------------------------------------
	// Rule 1：
	//
	// 完全没有产生 Chunk。
	//
	// 无论原文是什么，
	// 这都意味着当前 Splitter 没有正确工作。
	// -------------------------------------------------------------------------

	if len(chunks) == 0 {
		return ValidationResult{Reason: validationReasonNoChunks}
	}

	// -------------------------------------------------------------------------
	// Rule 2：
	//
	// 一个明显很大的文档，
	// 最后却只产生一个 Chunk。
	//
	// 例如：
	//
	//     ChunkSize = 512
	//     Document  = 5000 rune
	//
	// 最终：
	//
	//     chunks = 1
	//
	// 基本说明当前 Strategy 并没有真正完成分块。
	//
	// 当前 WeKnora 的判断条件：
	//
	//     totalChars > 2 * chunkSize
	//
	// 注意是严格 >。
	//
	// 如果：
	//
	//     totalChars == 2 * chunkSize
	//
	// 不会因为这一条被拒绝。
	// -------------------------------------------------------------------------

	if len(chunks) == 1 && totalChars > 2*chunkSize {
		return ValidationResult{Reason: validationReasonSingleLargeDocument}
	}

	// -------------------------------------------------------------------------
	// 接下来只需要统计：
	//
	//     最大 Chunk 长度
	//     Tiny Chunk 数量
	//
	// 当前 WeKnora 源码还计算了平均长度等统计量，
	// 但这些值目前没有参与最终 Validation Decision。
	//
	// 我们这里保留完全相同的“判定行为”，
	// 但不保留不会影响结果的无效中间计算，
	// 让代码更清楚。
	// -------------------------------------------------------------------------

	maxLen := 0
	tinyCount := 0

	for i, chunk := range chunks {
		length := RuneLen(chunk.Content)

		if length > maxLen {
			maxLen = length
		}

		// -----------------------------------------------------------------
		// Rule 3 所需要的 Tiny Chunk 统计。
		//
		// 这里特意不检查最后一个 Chunk。
		//
		// 因为文档尾部天然可能出现：
		//
		//     剩余 20 rune
		//     剩余 30 rune
		//
		// 这属于正常 tail residue，
		// 不能因为最后一块很小就判整个 Strategy 失败。
		// -----------------------------------------------------------------

		if i != len(chunks)-1 && length < tinyChunkThreshold {
			tinyCount++
		}
	}

	// -------------------------------------------------------------------------
	// Rule 3：
	//
	// 非尾部 Tiny Chunk 太多。
	//
	// 当前条件同时要求：
	//
	//     tinyCount > len(chunks) / 4
	//
	// 并且：
	//
	//     tinyCount > 2
	//
	// 为什么需要两个条件？
	//
	// 假设只有：
	//
	//     4 个 Chunk
	//
	// 其中一个比较小。
	//
	// 不能仅仅因为：
	//
	//     1 > 4/4 ?
	//
	// 就认为整个结果坏掉。
	//
	// tinyCount > 2
	//
	// 相当于再增加一道保险：
	//
	// 至少出现 3 个 Tiny Chunk，
	// 才值得认为存在系统性碎片化问题。
	//
	// Go 整数除法：
	//
	//     10 / 4 == 2
	//
	// 所以 10 个 Chunk 时，
	// Tiny 至少需要 3 个才可能触发。
	// -------------------------------------------------------------------------

	if tinyCount > len(chunks)/4 && tinyCount > 2 {
		return ValidationResult{Reason: validationReasonTooManyTinyChunks}
	}

	// -------------------------------------------------------------------------
	// Rule 4：
	//
	// 所有 Chunk 都远远小于目标尺寸。
	//
	// 判断方式并不是计算平均值，
	// 而是看：
	//
	//     最大的那个 Chunk
	//
	// 有没有达到：
	//
	//     ChunkSize / 4
	//
	// 如果连最大的块都不到目标值的 25%，
	// 并且整个文档本身又比 ChunkSize 大，
	//
	// 说明当前 Splitter 可能过度碎片化。
	//
	// 例如：
	//
	//     ChunkSize = 400
	//
	// 结果：
	//
	//     80
	//     75
	//     60
	//     90
	//
	// 最大：
	//
	//     90 < 100
	//
	// 这种结果会 Reject。
	//
	// totalChars > chunkSize 的限制也很重要：
	//
	// 如果整篇文章本来就只有 80 rune，
	// 最大 Chunk 只有 80 是完全正常的，
	// 不能因为目标 ChunkSize=512 就判错。
	// -------------------------------------------------------------------------

	if maxLen < chunkSize/minUsefulTargetDivisor && totalChars > chunkSize {
		return ValidationResult{Reason: validationReasonAllChunksTooSmall}
	}

	// -------------------------------------------------------------------------
	// Rule 5：
	//
	// 某个 Chunk 超过目标尺寸 2 倍。
	//
	// 例如：
	//
	//     ChunkSize = 512
	//
	// 最大 Chunk：
	//
	//     1500
	//
	// 这说明当前 Strategy 很可能没有正确遵守尺寸预算。
	//
	// 注意：
	//
	//     > 2 * chunkSize
	//
	// 是严格大于。
	//
	// 所以：
	//
	//     ChunkSize = 400
	//     maxLen    = 800
	//
	// 仍然允许。
	//
	//     maxLen = 801
	//
	// 才 Reject。
	//
	// chunkSize > 0 是防御性检查。
	//
	// 正常 Strategy 路径在调用 Validator 前
	// 已经经过 ensureDefaults()，
	// 所以实际生产路径 chunkSize 应该始终 > 0。
	// -------------------------------------------------------------------------

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
