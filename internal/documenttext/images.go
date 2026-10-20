package documenttext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tsawler/tabula"
	tabularag "github.com/tsawler/tabula/rag"
	"github.com/yuin/goldmark/ast"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// ImageInfo 只读取图片头；在任何位图解码、OCR 或视觉请求之前限制像素数。
// 返回的扩展名与实际编码一致，不相信上传文件名，也不把原图统一转成 JPEG。
func ImageInfo(data []byte, maxPixels int64) (mime, ext string, err error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf("document parser: invalid image: %w", err)
	}
	if maxPixels == 0 {
		maxPixels = DefaultOptions().MaxImagePixels
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > maxPixels/int64(cfg.Height) {
		return "", "", fmt.Errorf("%w: image dimensions", ErrResourceLimit)
	}
	switch format {
	case "jpeg":
		return "image/jpeg", ".jpg", nil
	case "png", "gif", "webp", "bmp":
		return "image/" + format, "." + format, nil
	case "tiff":
		return "image/tiff", ".tiff", nil
	default:
		return "", "", errors.New("document parser: unsupported image format")
	}
}

func IsImageExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff":
		return true
	}
	return false
}

func convertImage(filename string, opts ParserOptions) (Result, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Result{}, err
	}
	mime, ext, err := ImageInfo(data, opts.MaxImagePixels)
	if err != nil {
		return Result{}, err
	}
	if !opts.PreserveImages {
		text, err := recognizePage(data, opts.OCRLanguage)
		if err != nil {
			return Result{}, err
		}
		return checkedResult(Result{Markdown: text, SourceBlocks: textSourceBlocks(text, ".image")}, opts)
	}
	ref := "images/upload" + ext
	md := "![上传图片](" + ref + ")"
	locator := SourceLocator{Type: "image", Mapping: "exact", Image: ref}
	return checkedResult(Result{Markdown: md, Images: []ImageReference{{OriginalRef: ref, Filename: "上传图片", MIME: mime, Data: data, Locator: locator}},
		SourceBlocks: []SourceBlock{{Start: 0, End: utf8.RuneCountInString(md), Locator: locator}},
		Metadata:     map[string]any{"native_text_bytes": 0, "image_count": 1}}, opts)
}

// RecognitionImage 只调整视觉请求的派生图，不改写磁盘原件；不兼容的编码转换为 JPEG。
func RecognitionImage(data []byte, mime string, opts ParserOptions) ([]byte, string, error) {
	if _, _, err := ImageInfo(data, opts.effective().MaxImagePixels); err != nil {
		return nil, "", err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	pdf := opts.pdfOptions()
	if (mime == "image/png" || mime == "image/jpeg" || mime == "image/webp") && (pdf.RenderMaxEdge == 0 || max(cfg.Width, cfg.Height) <= pdf.RenderMaxEdge) {
		return data, mime, nil
	}
	data, err = pdfJPEG(data, pdf)
	return data, "image/jpeg", err
}

// inlineImage 只接受内嵌的真实图片数据，不读取 Markdown/HTML 指向的任意本地路径或网址。
func inlineImage(ref string, index int, opts ParserOptions) (ImageReference, error) {
	header, encoded, ok := strings.Cut(ref, ",")
	if !ok || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return ImageReference{}, errors.New("document parser: embedded image requires a Base64 data URI")
	}
	if base64.StdEncoding.DecodedLen(len(encoded)) > opts.MaxOutputBytes {
		return ImageReference{}, ErrResourceLimit
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ImageReference{}, fmt.Errorf("document parser: invalid embedded image: %w", err)
	}
	mime, ext, err := ImageInfo(data, opts.MaxImagePixels)
	if err != nil {
		return ImageReference{}, err
	}
	if !strings.EqualFold(header, "data:"+mime+";base64") {
		return ImageReference{}, errors.New("document parser: embedded image MIME mismatch")
	}
	name := fmt.Sprintf("inline-%d%s", index, ext)
	return ImageReference{OriginalRef: "images/" + name, Filename: name, MIME: mime, Data: data}, nil
}

// extractMarkdownImages 用 Goldmark 的节点范围替换内嵌图，代码块里的图片示例不会被当成附件。
func extractMarkdownImages(result *Result, opts ParserOptions) error {
	if !strings.Contains(strings.ToLower(result.Markdown), "data:image/") {
		return nil
	}
	parsed, err := ParseMarkdown(context.Background(), result.Markdown)
	if err != nil {
		return err
	}
	type replacement struct {
		start, end int
		image      ImageReference
	}
	var replacements []replacement
	var protected []MarkdownRange
	var references []string
	nativeBytes := 0
	imageBytes := 0
	err = ast.Walk(parsed.Root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch node.(type) {
			case *ast.CodeBlock, *ast.FencedCodeBlock:
				protected = append(protected, parsed.Range(node))
				for i := 0; i < node.Lines().Len(); i++ {
					segment := node.Lines().At(i)
					nativeBytes += segment.Len()
				}
				return ast.WalkSkipChildren, nil
			case *ast.CodeSpan:
				protected = append(protected, parsed.Range(node))
			case *ast.Text, *ast.String:
				nativeBytes += len(node.Text(parsed.Source))
			}
		}
		img, ok := node.(*ast.Image)
		if !entering || !ok || !strings.HasPrefix(strings.ToLower(string(img.Destination)), "data:image/") {
			return ast.WalkContinue, nil
		}
		if len(replacements) >= opts.MaxImages {
			return ast.WalkStop, ErrResourceLimit
		}
		remaining := opts
		remaining.MaxOutputBytes -= imageBytes
		asset, err := inlineImage(string(img.Destination), len(replacements)+1, remaining)
		if err != nil {
			return ast.WalkStop, err
		}
		imageBytes += len(asset.Data)
		r := parsed.Range(node)
		asset.Locator = SourceLocator{Type: "markdown", Mapping: "normalized_markdown", Image: asset.OriginalRef}
		replacements = append(replacements, replacement{r.Start, r.End, asset})
		references = append(references, string(img.Destination), asset.OriginalRef)
		return ast.WalkSkipChildren, nil
	})
	if err != nil {
		return err
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })
	sort.Slice(protected, func(i, j int) bool { return protected[i].Start < protected[j].Start })
	var out strings.Builder
	refs := strings.NewReplacer(references...)
	// Goldmark 已消解的引用定义没有独立节点；仅在非代码区域替换其完整 data URI，不手写定义语法。
	writeGap := func(start, end int) {
		for _, r := range protected {
			if r.End <= start {
				continue
			}
			if r.Start >= end {
				break
			}
			stop := max(start, r.Start)
			out.WriteString(refs.Replace(result.Markdown[start:stop]))
			start = min(r.End, end)
			out.WriteString(result.Markdown[stop:start])
		}
		out.WriteString(refs.Replace(result.Markdown[start:end]))
	}
	at := 0
	for _, r := range replacements {
		if r.start < at || r.end > len(result.Markdown) {
			return errors.New("document parser: invalid Markdown image range")
		}
		writeGap(at, r.start)
		out.WriteString("![文档图片](" + r.image.OriginalRef + ")")
		result.Images = append(result.Images, r.image)
		at = r.end
	}
	writeGap(at, len(result.Markdown))
	result.Markdown = out.String()
	result.SourceBlocks = textSourceBlocks(result.Markdown, ".md")
	result.Metadata = map[string]any{"native_text_bytes": nativeBytes}
	return nil
}

// convertHTMLImages 让标准 HTML 解析器定位 img；正文仍交给 Tabula，表格保护规则保持不变。
func convertHTMLImages(filename string, opts ParserOptions) (Result, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Result{}, err
	}
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return Result{}, err
	}
	result := Result{}
	var nodes []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			nodes = append(nodes, n)
		}
		if len(nodes) > opts.MaxImages {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if len(nodes) > opts.MaxImages {
				return
			}
			walk(c)
		}
	}
	walk(doc)
	if len(nodes) > opts.MaxImages {
		return Result{}, ErrResourceLimit
	}
	var replacements []string
	imageBytes := 0
	marker := "HUMBERTIMAGE" + fmt.Sprintf("%x", sha256.Sum256(data))
	for _, node := range nodes {
		ref, alt := "", ""
		for _, attr := range node.Attr {
			if attr.Key == "src" {
				ref = attr.Val
			}
			if attr.Key == "alt" {
				alt = attr.Val
			}
		}
		text := alt
		if strings.HasPrefix(strings.ToLower(ref), "data:image/") {
			if len(result.Images) >= opts.MaxImages {
				return Result{}, ErrResourceLimit
			}
			remaining := opts
			remaining.MaxOutputBytes -= imageBytes
			img, err := inlineImage(ref, len(result.Images)+1, remaining)
			if err != nil {
				return Result{}, err
			}
			imageBytes += len(img.Data)
			img.Locator = SourceLocator{Type: "html", Mapping: "normalized_markdown", Image: img.OriginalRef}
			result.Images = append(result.Images, img)
			text = fmt.Sprintf("%s%dEND", marker, len(result.Images))
			replacements = append(replacements, text, "![文档图片]("+img.OriginalRef+")")
		} else {
			result.Warnings = append(result.Warnings, tabula.Warning{Message: "HTML 外部/相对路径图片未自动读取；请提供内嵌图片或单独上传原图。"})
		}
		replacement := &html.Node{Type: html.TextNode, Data: text}
		// Tabula 按段落提取；body/div 下的裸文本会被省略，须将占位符放入实际段落。
		if node.Parent.Data != "p" {
			paragraph := &html.Node{Type: html.ElementNode, Data: "p", DataAtom: atom.P}
			paragraph.AppendChild(replacement)
			replacement = paragraph
		}
		node.Parent.InsertBefore(replacement, node)
		node.Parent.RemoveChild(node)
	}
	var source strings.Builder
	if err = html.Render(&source, doc); err != nil {
		return Result{}, err
	}
	md := tabularag.DefaultMarkdownOptions()
	md.IncludePageNumbers = opts.IncludePageNumbers
	markdown, warnings, err := convertHTML(filename, md, opts, source.String())
	if err != nil {
		return Result{}, err
	}
	result.Warnings = append(result.Warnings, warnings...)
	native := markdown
	for i := 0; i < len(replacements); i += 2 {
		native = strings.ReplaceAll(native, replacements[i], "")
	}
	result.Metadata = map[string]any{"native_text_bytes": len(strings.TrimSpace(native))}
	result.Markdown = normalizeMarkdown(strings.NewReplacer(replacements...).Replace(markdown))
	result.SourceBlocks = textSourceBlocks(result.Markdown, ".html")
	return checkedResult(result, opts)
}
