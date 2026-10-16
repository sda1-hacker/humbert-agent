package documenttext

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tsawler/tabula"
	"github.com/tsawler/tabula/pptx"
	tabularag "github.com/tsawler/tabula/rag"
	"github.com/tsawler/tabula/xlsx"
)

const (
	MetaSourceBlocks   = "rag_source_blocks"   // 仅在解析和切分之间传递，不复制到每个切片或文档主表。
	MetaSourceLocators = "rag_source_locators" // 每个切片只保存与自己相交的原文位置。
	maxSourceBlocks    = 20000                 // 定位元数据的防御上限；余下正文保留为明确标注的 Markdown 范围。
)

// SourceLocator 参照 WeKnora 的格式专用定位；没有可靠的版面坐标时不返回 BBox。
type SourceLocator struct {
	Type       string `json:"type"`
	Mapping    string `json:"mapping"` // exact 为解析正文精确匹配；normalized_markdown 仅定位转换后的正文。
	Partial    bool   `json:"partial,omitempty"`
	SourceHash string `json:"source_hash,omitempty"`
	Page       int    `json:"page,omitempty"` // PDF 起始页，1 起算；跨页片段同时返回 PageEnd。
	PageEnd    int    `json:"page_end,omitempty"`
	Block      int    `json:"block,omitempty"` // DOCX body 内的段落/表格序号，1 起算。
	Slide      int    `json:"slide,omitempty"` // PPTX 幻灯片序号，1 起算。
	Sheet      string `json:"sheet,omitempty"`
	RowStart   int    `json:"row_start,omitempty"` // XLSX/CSV 行号，1 起算；CSV 的一条多行记录算一行。
	RowEnd     int    `json:"row_end,omitempty"`
	Start      int    `json:"start"` // 最终 Markdown 的 Unicode 字符范围 [Start, End)。
	End        int    `json:"end"`
	Quote      string `json:"quote,omitempty"` // 命中范围的短引文；不是重复保存整篇原文。
}

type SourceBlock struct {
	Start   int           `json:"start"`
	End     int           `json:"end"`
	Locator SourceLocator `json:"locator"`
}

type sourceFragment struct {
	text    string
	locator SourceLocator
}

// convertStructured 只借用格式自身的结构信息；所有正文转换仍由 Tabula 完成。
func convertStructured(path string, extractor *tabula.Extractor, md tabularag.MarkdownOptions, opts ParserOptions) (string, []tabula.Warning, []sourceFragment, error) {
	var fragments []sourceFragment
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm":
		markdown, warnings, err := convertHTML(path, md, opts)
		return markdown, warnings, nil, err
	case ".pdf":
		collection, warnings, err := extractor.Chunks()
		if err != nil {
			return "", warnings, nil, err
		}
		for _, chunk := range collection.Chunks {
			fragments = append(fragments, sourceFragment{chunk.Text, SourceLocator{Type: "pdf", Page: chunk.Metadata.PageStart, PageEnd: chunk.Metadata.PageEnd}})
			if len(fragments) >= maxSourceBlocks {
				break
			}
		}
		return collection.ToMarkdownWithOptions(md), warnings, fragments, nil
	case ".pptx":
		reader, err := pptx.Open(path)
		if err != nil {
			return "", nil, nil, err
		}
		defer reader.Close()
		extract := pptx.ExtractOptions{IncludeNotes: true, IncludeTitles: true, ExcludeHeaders: opts.ExcludeHeadersAndFooters, ExcludeFooters: opts.ExcludeHeadersAndFooters}
		order, err := presentationOrder(path, reader)
		if err != nil {
			return "", nil, nil, err
		}
		var markdown strings.Builder
		for i, nativeIndex := range order {
			extract.SlideNumbers = []int{nativeIndex}
			part, err := reader.MarkdownWithRAGOptions(extract, md)
			if err != nil {
				return "", nil, nil, err
			}
			// 逐页渲染一次，边合并边限制输出；避免重复转换和恶意重复页引用放大内存。
			remaining := opts.MaxOutputBytes - markdown.Len()
			if i > 0 {
				remaining -= len("\n\n---\n\n")
			}
			if len(part) > remaining {
				return "", nil, nil, ErrResourceLimit
			}
			if i > 0 {
				markdown.WriteString("\n\n---\n\n")
			}
			markdown.WriteString(part)
			fragments = append(fragments, sourceFragment{part, SourceLocator{Type: "slide", Slide: i + 1}})
		}
		return markdown.String(), nil, fragments, nil
	case ".xlsx":
		reader, err := xlsx.Open(path)
		if err != nil {
			return "", nil, nil, err
		}
		defer reader.Close()
		markdown, err := reader.MarkdownWithRAGOptions(xlsx.ExtractOptions{}, md)
		if err != nil || len(markdown) > opts.MaxOutputBytes {
			return markdown, nil, nil, err
		}
		for i := 0; i < reader.SheetCount() && len(fragments) < maxSourceBlocks; i++ {
			sheet, err := reader.Sheet(i)
			if err != nil {
				return "", nil, nil, err
			}
			part, err := reader.MarkdownWithRAGOptions(xlsx.ExtractOptions{Sheets: []int{i}}, md)
			if err != nil {
				return "", nil, nil, err
			}
			// Tabula 去掉工作表首尾空行，随后逐行输出；从真实非空行起算，不把表头分隔线算成 Excel 行。
			first := -1
			for row, cells := range sheet.Rows {
				for _, cell := range cells {
					if !cell.IsEmpty() {
						first = row
						break
					}
				}
				if first >= 0 {
					break
				}
			}
			row := first + 1
			separator := false
			for line := range strings.SplitSeq(part, "\n") {
				if len(fragments) >= maxSourceBlocks {
					break
				}
				locator := SourceLocator{Type: "sheet", Sheet: sheet.Name}
				if strings.HasPrefix(line, "|") {
					if separator {
						separator = false
						continue
					}
					locator.RowStart, locator.RowEnd = row, row
					separator = row == first+1
					row++
				}
				fragments = append(fragments, sourceFragment{line, locator})
			}
		}
		return markdown, nil, fragments, nil
	default:
		markdown, warnings, err := extractor.ToMarkdownWithOptions(md)
		if err == nil && len(markdown) <= opts.MaxOutputBytes && strings.EqualFold(filepath.Ext(path), ".docx") {
			fragments = docxFragments(path)
		}
		return markdown, warnings, fragments, err
	}
}

// presentationOrder 补齐 Tabula v1.6.14 的顺序差异：它按 slide 文件编号解析，实际放映顺序由 sldIdLst 决定。
// 通过公开的 Slide.Index 和 SlideNumbers 重新排序，不修改第三方库，也不自行转换幻灯片正文。
func presentationOrder(filename string, reader *pptx.Reader) ([]int, error) {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	var parts []string
	for _, file := range archive.File {
		if strings.HasPrefix(file.Name, "ppt/slides/slide") && strings.HasSuffix(file.Name, ".xml") && !strings.Contains(file.Name, "_rels") {
			parts = append(parts, file.Name)
		}
	}
	number := func(part string) int {
		var n int
		fmt.Sscanf(strings.TrimSuffix(strings.TrimPrefix(part, "ppt/slides/slide"), ".xml"), "%d", &n)
		return n
	}
	sort.Slice(parts, func(i, j int) bool { return number(parts[i]) < number(parts[j]) })
	native := make(map[string]int, reader.SlideCount())
	for i := 0; i < reader.SlideCount(); i++ {
		slide, err := reader.Slide(i)
		if err != nil || slide.Index < 0 || slide.Index >= len(parts) {
			return nil, fmt.Errorf("document parser: cannot map PPTX slide %d", i)
		}
		native[parts[slide.Index]] = i
	}
	rels, err := archive.Open("ppt/_rels/presentation.xml.rels")
	if err != nil {
		return nil, err
	}
	defer rels.Close()
	var relationships struct {
		Items []struct {
			ID         string `xml:"Id,attr"`
			Target     string `xml:"Target,attr"`
			Type       string `xml:"Type,attr"`
			TargetMode string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.NewDecoder(rels).Decode(&relationships); err != nil {
		return nil, err
	}
	byID := make(map[string]string)
	for _, rel := range relationships.Items {
		if strings.HasSuffix(rel.Type, "/slide") && rel.TargetMode != "External" {
			target := strings.TrimPrefix(rel.Target, "/")
			if !strings.HasPrefix(rel.Target, "/") {
				target = path.Join("ppt", target)
			}
			byID[rel.ID] = path.Clean(target)
		}
	}
	file, err := archive.Open("ppt/presentation.xml")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	var order []int
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if element, ok := token.(xml.StartElement); ok && element.Name.Local == "sldId" {
			found := false
			for _, attr := range element.Attr {
				if attr.Name.Local == "id" && strings.HasSuffix(attr.Name.Space, "/relationships") {
					index, ok := native[byID[attr.Value]]
					if !ok {
						return nil, fmt.Errorf("document parser: referenced PPTX slide %q could not be parsed", attr.Value)
					}
					order = append(order, index)
					found = true
					if len(order) > maxSourceBlocks {
						return nil, ErrResourceLimit
					}
				}
			}
			if !found {
				return nil, fmt.Errorf("document parser: PPTX slide is missing its relationship reference")
			}
		}
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("document parser: PPTX has no presentation slide order")
	}
	return order, nil
}

// docxFragments 只读取 Word body 的段落锚点，不参与格式转换；列表和表格内的段落沿用 body 所在块号。
func docxFragments(path string) []sourceFragment {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil
	}
	defer archive.Close()
	file, err := archive.Open("word/document.xml")
	if err != nil {
		return nil
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	depth, body, paragraph, block := 0, -1, -1, 0
	var text strings.Builder
	var fragments []sourceFragment
	for {
		token, err := decoder.Token()
		if err != nil {
			return fragments
		}
		switch token := token.(type) {
		case xml.StartElement:
			depth++
			if token.Name.Local == "body" {
				body = depth
			} else if body >= 0 && depth == body+1 && (token.Name.Local == "p" || token.Name.Local == "tbl") {
				block++
			}
			if body >= 0 && token.Name.Local == "p" {
				paragraph = depth
				text.Reset()
			}
			if paragraph >= 0 && token.Name.Local == "t" {
				var value string
				if decoder.DecodeElement(&value, &token) == nil {
					text.WriteString(value)
				}
				depth--
			}
		case xml.EndElement:
			if paragraph == depth && token.Name.Local == "p" {
				fragments = append(fragments, sourceFragment{text.String(), SourceLocator{Type: "docx", Block: block}})
				if len(fragments) >= maxSourceBlocks {
					return fragments
				}
				paragraph = -1
			}
			if body == depth && token.Name.Local == "body" {
				body = -1
			}
			depth--
		}
	}
}

// alignSourceBlocks 只接受顺序一致的精确匹配；清洗改写后无法可靠匹配的区域退回正文范围，不猜测原始位置。
func alignSourceBlocks(markdown string, fragments []sourceFragment) []SourceBlock {
	var blocks []SourceBlock
	bytePos, runePos, misses := 0, 0, 0
	for _, fragment := range fragments[:min(len(fragments), maxSourceBlocks)] {
		quote := strings.TrimSpace(normalizeMarkdown(fragment.text))
		if quote == "" {
			continue
		}
		index := strings.Index(markdown[bytePos:], quote)
		if index < 0 {
			misses++
			if misses >= 32 {
				break // 限制坏文档反复全文查找的开销；剩余部分使用明确标注的正文位置。
			}
			continue
		}
		start := runePos + utf8.RuneCountInString(markdown[bytePos:bytePos+index])
		end := start + utf8.RuneCountInString(quote)
		if start > runePos {
			blocks = append(blocks, fallbackBlock(runePos, start))
		}
		blocks = append(blocks, SourceBlock{Start: start, End: end, Locator: fragment.locator})
		bytePos += index + len(quote)
		runePos = end
	}
	if len(blocks) == 0 {
		return textSourceBlocks(markdown, "")
	}
	if runePos < utf8.RuneCountInString(markdown) {
		blocks = append(blocks, fallbackBlock(runePos, utf8.RuneCountInString(markdown)))
	}
	for i := range blocks {
		if blocks[i].Locator.Mapping == "" {
			blocks[i].Locator.Mapping = "exact"
		}
		blocks[i].Locator.Start, blocks[i].Locator.End = blocks[i].Start, blocks[i].End
	}
	return blocks
}

func fallbackBlock(start, end int) SourceBlock {
	return SourceBlock{Start: start, End: end, Locator: SourceLocator{Type: "text", Mapping: "normalized_markdown", Partial: true, Start: start, End: end}}
}

func textSourceBlocks(markdown, ext string) []SourceBlock {
	var blocks []SourceBlock
	offset, row := 0, 1
	i := -1
	for line := range strings.SplitAfterSeq(markdown, "\n") {
		i++
		end := offset + utf8.RuneCountInString(line)
		if len(blocks) >= maxSourceBlocks {
			blocks = append(blocks, fallbackBlock(offset, utf8.RuneCountInString(markdown)))
			break
		}
		if strings.TrimSpace(line) != "" {
			locator := SourceLocator{Type: "text", Mapping: "normalized_markdown", Partial: true, Start: offset, End: end}
			if ext == ".csv" {
				if i == 1 { // Markdown 分隔线没有对应的 CSV 行。
					offset = end
					continue
				}
				locator.Type, locator.Mapping, locator.Partial = "sheet", "exact", false
				locator.RowStart, locator.RowEnd = row, row
				row++
			}
			blocks = append(blocks, SourceBlock{Start: offset, End: end, Locator: locator})
		}
		offset = end
	}
	return blocks
}

// SliceLocators 在最终切片范围上重新求交；预算二次切分、父块展开后也能保留正确的引用范围。
func SliceLocators(blocks []SourceBlock, start, end int, sourceHash string) []SourceLocator {
	var result []SourceLocator
	index := sort.Search(len(blocks), func(i int) bool { return blocks[i].End > start })
	for _, block := range blocks[index:] {
		if block.Start >= end {
			break
		}
		locator := block.Locator
		locator.Start, locator.End = max(start, block.Start), min(end, block.End)
		locator.SourceHash = sourceHash
		locator.Partial = locator.Partial || locator.Start != block.Start || locator.End != block.End || locator.PageEnd > locator.Page
		result = append(result, locator)
	}
	return result
}

// AttachLocators 丢弃全文位置列表，仅留下当前切片的交集；预算二次切分时继续裁剪已有位置。
func AttachLocators(metadata map[string]any, start, end int) {
	blocks, ok := metadata[MetaSourceBlocks].([]SourceBlock)
	if !ok {
		if locators, ok := metadata[MetaSourceLocators].([]SourceLocator); ok {
			blocks = make([]SourceBlock, len(locators))
			for i, locator := range locators {
				blocks[i] = SourceBlock{Start: locator.Start, End: locator.End, Locator: locator}
			}
		}
	}
	delete(metadata, MetaSourceBlocks)
	hash, _ := metadata["rag_source_content_hash"].(string)
	if locators := SliceLocators(blocks, start, end, hash); len(locators) > 0 {
		metadata[MetaSourceLocators] = locators
	} else {
		delete(metadata, MetaSourceLocators)
	}
}
