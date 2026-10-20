package documenttext

import (
	"errors"
	"math"
	"net/url"
)

// PDFOptions 对齐 WeKnora 2c0d838 的 builtin PDF 参数；不混入分块窗口或检索规则。
// PDFium 和 Tabula 提供的底层信息不同：这里仅暴露实际执行的参数，不声明未实现的隐藏字形过滤或矢量图识别。
type PDFOptions struct {
	// PaddleOCRVLEndpoint 留空使用 Tabula；配置后将 PDF 原件发送给该结构化解析服务的 /layout-parsing。
	// 这是可选的 PaddleOCR-VL pipeline 地址，不是普通视觉模型的 OpenAI 接口；跨页合表由该引擎完成。
	PaddleOCRVLEndpoint     string  `mapstructure:"paddleocr_vl_endpoint"`
	ScanImageRatio          float64 `mapstructure:"scan_image_ratio"`           // 图片覆盖页面积 >= 0.5 视为扫描页，允许重叠图片总面积 > 1。
	ScanMinChars            int     `mapstructure:"scan_min_chars"`             // 正文不足 10 字符且图片覆盖 >= 0.1 时同样进入扫描路径。
	ForceScanned            bool    `mapstructure:"force_scanned"`              // 强制所有页面进入扫描路径，默认 false。
	LayoutOrdering          bool    `mapstructure:"layout_ordering"`            // 默认 true；恢复分栏阅读顺序，质量不足时回退文本层。
	WordGapWidthRatio       float64 `mapstructure:"word_gap_width_ratio"`       // 默认 0.4；按文字宽度推断单词间空格。
	DetectHeadings          bool    `mapstructure:"detect_headings"`            // 默认 true；按相对字号提升标题，不推断章节编号层级。
	MinHeadingLineChars     int     `mapstructure:"min_heading_line_chars"`     // 标题最少 8 字符；最多 80，句末标点行不提升。
	MarginColWidthRatio     float64 `mapstructure:"margin_col_width_ratio"`     // 默认 0.12；辅助排除狭窄的竖排水印列。
	SanitizeText            bool    `mapstructure:"sanitize_text"`              // 默认 true；清理 PDF 文本层的软连字符、零宽字符等占位符。
	StripChartDebris        bool    `mapstructure:"strip_chart_debris"`         // 默认 true；清理连续图表轴刻度/图例残片。
	ExtractEmbeddedImages   bool    `mapstructure:"extract_embedded_images"`    // 默认 true；提取正文页插图，原始文档始终另外保存。
	EmbedMinPixels          int     `mapstructure:"embed_min_pixels"`           // 插图宽、高均至少 80 像素。
	EmbedMinAreaRatio       float64 `mapstructure:"embed_min_area_ratio"`       // 插图至少占页面面积的 0.01。
	EmbedRepeatPageFraction float64 `mapstructure:"embed_repeat_page_fraction"` // 至少 50% 正文页重复的图片视为页徽/水印。
	EmbedMaxImages          int     `mapstructure:"embed_max_images"`           // 最多提取 50 张正文插图；不限制扫描页数。
	RenderDPI               int     `mapstructure:"render_dpi"`                 // 内嵌 PDFium 渲染扫描页，默认 200 DPI，无需安装外部程序。
	JPEGQuality             int     `mapstructure:"jpeg_quality"`               // 扫描页和插图 JPEG 质量 85，范围 1..95。
	RenderMaxEdge           int     `mapstructure:"render_max_edge"`            // 渲染最长边 2000 像素，0 关闭缩放上限。
}

func DefaultPDFOptions() PDFOptions {
	return PDFOptions{ScanImageRatio: .5, ScanMinChars: 10, LayoutOrdering: true,
		WordGapWidthRatio: .4, DetectHeadings: true, MinHeadingLineChars: 8,
		MarginColWidthRatio: .12, SanitizeText: true, StripChartDebris: true,
		ExtractEmbeddedImages: true, EmbedMinPixels: 80, EmbedMinAreaRatio: .01,
		EmbedRepeatPageFraction: .5, EmbedMaxImages: 50, RenderDPI: 200, JPEGQuality: 85, RenderMaxEdge: 2000}
}

func (o PDFOptions) Validate() error {
	if o.PaddleOCRVLEndpoint != "" {
		u, err := url.Parse(o.PaddleOCRVLEndpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("document parser: PaddleOCR-VL endpoint must be an HTTP(S) base URL without credentials, query or fragment")
		}
	}
	for _, n := range []float64{o.ScanImageRatio, o.WordGapWidthRatio, o.MarginColWidthRatio, o.EmbedMinAreaRatio, o.EmbedRepeatPageFraction} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n > 1 {
			return errors.New("document parser: PDF ratios must be in (0,1]")
		}
	}
	if o.ScanMinChars < 0 || o.MinHeadingLineChars < 1 || o.MinHeadingLineChars > 80 || o.EmbedMinPixels < 1 || o.EmbedMaxImages < 1 || o.RenderDPI < 1 || o.RenderDPI > 600 || o.JPEGQuality < 1 || o.JPEGQuality > 95 || o.RenderMaxEdge < 0 {
		return errors.New("document parser: invalid PDF size, heading or rendering parameter")
	}
	return nil
}

func (o ParserOptions) pdfOptions() PDFOptions {
	if o.PDF != nil {
		return *o.PDF
	}
	return DefaultPDFOptions()
}
