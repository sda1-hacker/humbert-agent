package documenttext

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/tsawler/tabula"
)

type packageRelation struct{ ID, Target, Type, TargetMode string }
type imagePart struct {
	name    string
	locator SourceLocator
}

// extractOfficeImages 只补齐 Tabula 未提供的包内图片关系，不重复解析或渲染正文。
// ZIP 已在 worker 中预检；只读取正文实际引用的媒体，不访问外部关系或文件系统路径。
func extractOfficeImages(filename string, opts ParserOptions, result *Result) error {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext != ".docx" && ext != ".pptx" && ext != ".xlsx" && ext != ".odt" && ext != ".epub" {
		return nil
	}
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer archive.Close()
	files := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		files[file.Name] = file
	}
	readXML := func(name string, visit func(xml.StartElement, int) error) error {
		file := files[name]
		if file == nil {
			return fmt.Errorf("document parser: missing package part %s", name)
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		defer in.Close()
		decoder := xml.NewDecoder(io.LimitReader(in, int64(min(opts.MaxExpandedBytes, uint64(1<<63-1)))))
		depth, body, block := 0, -1, 0
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			switch token := token.(type) {
			case xml.StartElement:
				depth++
				if token.Name.Local == "body" {
					body = depth
				}
				if body >= 0 && depth == body+1 && (token.Name.Local == "p" || token.Name.Local == "tbl") {
					block++
				}
				if err := visit(token, block); err != nil {
					return err
				}
			case xml.EndElement:
				if body == depth && token.Name.Local == "body" {
					body = -1
				}
				depth--
			}
		}
	}
	relations := func(part string) (map[string]packageRelation, error) {
		rels := map[string]packageRelation{}
		name := path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
		if files[name] == nil {
			return rels, nil
		}
		err := readXML(name, func(e xml.StartElement, _ int) error {
			if e.Name.Local != "Relationship" {
				return nil
			}
			r := packageRelation{ID: xmlAttribute(e, "Id"), Target: xmlAttribute(e, "Target"), Type: xmlAttribute(e, "Type"), TargetMode: xmlAttribute(e, "TargetMode")}
			rels[r.ID] = r
			return nil
		})
		return rels, err
	}
	parts := []imagePart{}
	root, tag, kind := "", "", ""
	switch ext {
	case ".docx":
		parts = append(parts, imagePart{"word/document.xml", SourceLocator{Type: "docx"}})
	case ".odt":
		parts = append(parts, imagePart{"content.xml", SourceLocator{Type: "odt"}})
	case ".pptx":
		root, tag, kind = "ppt/presentation.xml", "sldId", "slide"
	case ".xlsx":
		root, tag, kind = "xl/workbook.xml", "sheet", "worksheet"
	case ".epub":
		for _, file := range archive.File {
			if strings.HasSuffix(strings.ToLower(file.Name), ".xhtml") || strings.HasSuffix(strings.ToLower(file.Name), ".html") {
				parts = append(parts, imagePart{file.Name, SourceLocator{Type: "epub", Mapping: "package_part", Partial: true}})
			}
		}
	}
	if root != "" {
		rels, err := relations(root)
		if err != nil {
			return err
		}
		err = readXML(root, func(e xml.StartElement, _ int) error {
			if e.Name.Local != tag {
				return nil
			}
			r := rels[relationshipID(e)]
			if !strings.HasSuffix(r.Type, "/"+kind) || r.TargetMode == "External" {
				return nil
			}
			name, ok := packageTarget(root, r.Target)
			if !ok {
				return fmt.Errorf("document parser: invalid part target")
			}
			locator := SourceLocator{Type: "slide", Slide: len(parts) + 1}
			if ext == ".xlsx" {
				locator = SourceLocator{Type: "sheet", Sheet: xmlAttribute(e, "name")}
			}
			parts = append(parts, imagePart{name, locator})
			return nil
		})
		if err != nil {
			return err
		}
	}
	usedBytes := len(result.Markdown)
	var images []ImageReference
	add := func(name string, locator SourceLocator) error {
		if len(images) >= opts.MaxImages {
			return fmt.Errorf("%w: too many document images", ErrResourceLimit)
		}
		file := files[name]
		if file == nil {
			return fmt.Errorf("document parser: missing embedded image %s", name)
		}
		if file.UncompressedSize64 > uint64(max(0, opts.MaxOutputBytes-usedBytes)) {
			return ErrResourceLimit
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(in, int64(opts.MaxOutputBytes-usedBytes)+1))
		in.Close()
		if err != nil {
			return err
		}
		if len(data) > opts.MaxOutputBytes-usedBytes {
			return ErrResourceLimit
		}
		mime, suffix, err := ImageInfo(data, opts.MaxImagePixels)
		if err != nil {
			if !IsImageExtension(path.Ext(name)) {
				result.Warnings = append(result.Warnings, tabula.Warning{Message: "未识别的嵌入图片编码：" + path.Base(name) + "；原文档已保留。"})
				return nil
			}
			return err
		}
		usedBytes += len(data)
		ref := fmt.Sprintf("images/embedded-%d%s", len(images)+1, suffix)
		locator.Image = ref
		if locator.Mapping == "" {
			locator.Mapping = "embedded_image"
		}
		images = append(images, ImageReference{OriginalRef: ref, Filename: path.Base(name), MIME: mime, Data: data, Locator: locator})
		return nil
	}
	var walk func(imagePart, int) error
	walk = func(part imagePart, depth int) error {
		if depth > 4 {
			return fmt.Errorf("%w: cyclic package drawing", ErrResourceLimit)
		}
		rels, err := relations(part.name)
		if err != nil {
			return err
		}
		return readXML(part.name, func(e xml.StartElement, block int) error {
			if (ext == ".odt" && e.Name.Local == "image") || (ext == ".epub" && e.Name.Local == "img") {
				target := xmlAttribute(e, "href")
				if ext == ".epub" {
					target = xmlAttribute(e, "src")
				}
				if name, ok := packageTarget(part.name, target); ok {
					return add(name, part.locator)
				}
				return nil
			}
			id := relationshipID(e)
			if e.Name.Local == "blip" {
				id = xmlAttribute(e, "embed")
			}
			r, ok := rels[id]
			if !ok {
				return nil
			}
			if strings.EqualFold(r.TargetMode, "External") {
				if strings.HasSuffix(r.Type, "/image") {
					result.Warnings = append(result.Warnings, tabula.Warning{Message: "外部链接图片未自动下载，当前只索引包内图片。"})
				}
				return nil
			}
			name, ok := packageTarget(part.name, r.Target)
			if !ok {
				return fmt.Errorf("document parser: invalid embedded image target")
			}
			locator := part.locator
			if ext == ".docx" {
				locator.Block = block
			} // body 块号与正文定位相同，不冒充 Word 的页码。
			if strings.HasSuffix(r.Type, "/image") {
				return add(name, locator)
			}
			if strings.HasSuffix(r.Type, "/drawing") || strings.HasSuffix(r.Type, "/vmlDrawing") {
				return walk(imagePart{name, locator}, depth+1)
			}
			return nil
		})
	}
	for _, part := range parts {
		if err := walk(part, 0); err != nil {
			return err
		}
	}
	if result.Metadata == nil {
		result.Metadata = map[string]any{}
	}
	result.Metadata["native_text_bytes"] = len(strings.TrimSpace(result.Markdown))
	result.Metadata["embedded_image_count"] = len(images)
	insertImageReferences(result, images)
	return nil
}

func xmlAttribute(e xml.StartElement, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}
func relationshipID(e xml.StartElement) string {
	for _, a := range e.Attr {
		if a.Name.Local == "id" && strings.Contains(a.Name.Space, "relationships") {
			return a.Value
		}
	}
	return ""
}

func packageTarget(part, target string) (string, bool) {
	u, err := url.Parse(target)
	if err != nil || target == "" || u.IsAbs() || u.Host != "" || u.RawQuery != "" || strings.Contains(u.Path, "\\") {
		return "", false
	}
	name := path.Clean(path.Join(path.Dir(part), u.Path))
	if strings.HasPrefix(u.Path, "/") {
		name = path.Clean(strings.TrimPrefix(u.Path, "/"))
	}
	return name, name != "." && name != ".." && !strings.HasPrefix(name, "../")
}

// insertImageReferences 尽量放回所属段落/幻灯片/工作表之后，并同步重建字符范围。
// 图片独有的文字由后续识别步骤补充；不把图片路径当作识别成功的正文。
func insertImageReferences(result *Result, images []ImageReference) {
	if len(images) == 0 {
		return
	}
	groups := map[int][]ImageReference{}
	for _, img := range images {
		at := len(result.SourceBlocks) - 1
		for i, block := range result.SourceBlocks {
			a, b := img.Locator, block.Locator
			if a.Type == b.Type && ((a.Slide > 0 && a.Slide == b.Slide) || (a.Sheet != "" && a.Sheet == b.Sheet) || (a.Block > 0 && b.Block > 0 && b.Block <= a.Block)) {
				at = i
			}
		}
		groups[at] = append(groups[at], img)
	}
	source := []rune(result.Markdown)
	var out strings.Builder
	var blocks []SourceBlock
	offset, last := 0, 0
	write := func(text string) { out.WriteString(text); offset += utf8.RuneCountInString(text) }
	appendImages := func(index int) {
		for _, img := range groups[index] {
			write("\n\n")
			start := offset
			write("![文档图片](" + img.OriginalRef + ")")
			locator := img.Locator
			locator.Start, locator.End = start, offset
			blocks = append(blocks, SourceBlock{start, offset, locator})
			write("\n\n")
		}
	}
	for i, block := range result.SourceBlocks {
		write(string(source[last:block.Start]))
		block.Start = offset
		original := result.SourceBlocks[i]
		write(string(source[original.Start:original.End]))
		block.End = offset
		block.Locator.Start, block.Locator.End = block.Start, block.End
		blocks = append(blocks, block)
		last = original.End
		appendImages(i)
	}
	write(string(source[last:]))
	appendImages(-1)
	result.Markdown, result.SourceBlocks = out.String(), blocks
	result.Images = append(result.Images, images...)
}
