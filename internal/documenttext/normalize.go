package documenttext

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gohugoio/hugo-goldmark-extensions/passthrough"
	"github.com/tsawler/tabula"
	tabularag "github.com/tsawler/tabula/rag"
	"github.com/yuin/goldmark/ast"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var htmlRowStart = regexp.MustCompile(`\s*<tr\b`)

// convertHTML 让 Tabula 转换正文，并在转换前保留表格片段，防止库先把合并关系和单元格引用压平。
func convertHTML(path string, md tabularag.MarkdownOptions, opts ParserOptions, sources ...string) (string, []tabula.Warning, error) {
	source := ""
	if len(sources) > 0 {
		source = sources[0]
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil, err
		}
		source = string(data)
	}
	var masked strings.Builder
	var replacements []string
	consumed := 0
	marker := "HUMBERTTABLE" + rand.Text()
	for i, location := range HTMLTableRanges(source) {
		token := marker + strconv.Itoa(i) + "END"
		masked.WriteString(source[consumed:location[0]])
		masked.WriteString("<p>" + token + "</p>")
		replacements = append(replacements, token, normalizeTable(source[location[0]:location[1]]))
		consumed = location[1]
	}
	masked.WriteString(source[consumed:])
	extractor := tabula.FromHTMLString(masked.String())
	if opts.ExcludeHeadersAndFooters {
		extractor = extractor.ExcludeHeadersAndFooters()
	}
	markdown, warnings, err := extractor.ToMarkdownWithOptions(md)
	if err == nil && len(replacements) > 0 {
		markdown = strings.NewReplacer(replacements...).Replace(markdown)
	}
	return markdown, warnings, err
}

// normalizeMarkdown 在建立原文范围之前统一换行和 HTML 表格；代码示例中的 HTML 保持原样。
func normalizeMarkdown(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if !strings.Contains(strings.ToLower(text), "<table") {
		return text
	}
	md, err := ParseMarkdown(context.Background(), text)
	if err != nil {
		return text
	}
	// 代码、公式和链接中的 HTML 是示例文字，不能被表格清洗误改。
	var protected []MarkdownRange
	_ = ast.Walk(md.Root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch n.(type) {
			case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.CodeSpan, *ast.Link, *ast.Image, *passthrough.PassthroughBlock, *passthrough.PassthroughInline:
				protected = append(protected, md.Range(n))
				return ast.WalkSkipChildren, nil
			}
		}
		return ast.WalkContinue, nil
	})
	sort.Slice(protected, func(i, j int) bool { return protected[i].Start < protected[j].Start })
	var out strings.Builder
	at := 0
	for _, r := range protected {
		if r.Start < at || r.End <= r.Start {
			continue
		}
		out.WriteString(normalizeTables(text[at:r.Start]))
		out.WriteString(text[r.Start:r.End])
		at = r.End
	}
	out.WriteString(normalizeTables(text[at:]))
	return out.String()
}

func normalizeTables(text string) string {
	var out strings.Builder
	consumed := 0
	for _, location := range HTMLTableRanges(text) {
		out.WriteString(text[consumed:location[0]])
		out.WriteString(normalizeTable(text[location[0]:location[1]]))
		consumed = location[1]
	}
	if consumed == 0 {
		return text
	}
	out.WriteString(text[consumed:])
	return out.String()
}

// HTMLTableRanges 返回完整外层表格的字节范围，供清洗与切分共用，支持嵌套表格。
func HTMLTableRanges(text string) [][2]int {
	if !strings.Contains(strings.ToLower(text), "<table") {
		return nil
	}
	// HTML tokenizer 确认完整的外层 table，避免正则在嵌套表格的第一个 </table> 提前结束。
	tokenizer := html.NewTokenizer(strings.NewReader(text))
	offset, start, depth := 0, 0, 0
	var locations [][2]int
	for {
		kind := tokenizer.Next()
		raw := tokenizer.Raw()
		previous := offset
		offset += len(raw)
		if kind == html.ErrorToken {
			break
		}
		if kind != html.StartTagToken && kind != html.EndTagToken {
			continue
		}
		name, _ := tokenizer.TagName()
		if string(name) != "table" {
			continue
		}
		if kind == html.StartTagToken {
			if depth == 0 {
				start = previous
			}
			depth++
		} else if depth > 0 {
			depth--
			if depth == 0 {
				locations = append(locations, [2]int{start, offset})
			}
		}
	}
	return locations
}

func normalizeTable(source string) string {
	nodes, err := html.ParseFragment(strings.NewReader(source), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return source
	}
	merged, rich, tables := false, false, 0
	var clean func(*html.Node)
	clean = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if node.Parent != nil && node.Parent.Data == "table" && (node.Data == "thead" || node.Data == "tbody" || node.Data == "tfoot") {
				clampRowSpans(node)
			}
			if node.Data == "table" {
				tables++
			}
			rich = rich || node.Data == "a" || node.Data == "img"
			attrs := node.Attr[:0]
			for _, attr := range node.Attr {
				if attr.Key == "rowspan" || attr.Key == "colspan" {
					if size, err := strconv.Atoi(strings.TrimSpace(attr.Val)); err == nil && (size > 1 || (attr.Key == "rowspan" && size == 0)) {
						attrs = append(attrs, html.Attribute{Key: attr.Key, Val: strconv.Itoa(size)})
						merged = true
					}
				} else if (node.Data == "a" && attr.Key == "href") ||
					(node.Data == "img" && (attr.Key == "src" || attr.Key == "alt")) {
					attrs = append(attrs, attr)
				}
			}
			node.Attr = attrs
		}
		for child := node.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == html.ElementNode && (child.Data == "script" || child.Data == "style") {
				node.RemoveChild(child)
			} else {
				clean(child)
				// 展平表格行组，保留 th/rowspan/colspan；续块只需补 table 和表头，不会留下半个 tbody。
				if node.Data == "table" && (child.Data == "thead" || child.Data == "tbody" || child.Data == "tfoot") {
					for row := child.FirstChild; row != nil; {
						following := row.NextSibling
						if child.Data == "thead" {
							for cell := row.FirstChild; cell != nil; cell = cell.NextSibling {
								if cell.Type == html.ElementNode && cell.Data == "td" {
									cell.Data, cell.DataAtom = "th", atom.Th
								}
							}
						}
						child.RemoveChild(row)
						node.InsertBefore(row, child)
						row = following
					}
					node.RemoveChild(child)
				}
			}
			child = next
		}
	}
	var out strings.Builder
	for _, node := range nodes {
		clean(node)
		if err := html.Render(&out, node); err != nil {
			return source
		}
	}
	if merged || rich || tables > 1 {
		// 合并单元格、嵌套及含链接/图片的表保留 HTML，避免 Tabula 的普通表格渲染丢掉引用。
		// 只保留引用，不下载图片；换行归一化保持幂等，重复处理不会不断增加空行。
		return htmlRowStart.ReplaceAllString(out.String(), "\n<tr")
	}
	opts := tabularag.DefaultMarkdownOptions()
	markdown, _, err := tabula.FromHTMLString(out.String()).ToMarkdownWithOptions(opts)
	if err != nil || !strings.Contains(markdown, "|") {
		return out.String()
	}
	return "\n\n" + strings.TrimSpace(markdown) + "\n\n"
}

// clampRowSpans 在展平前固定行组范围；rowspan=0 表示当前行组剩余行，不能错误延伸到下一个 tbody。
func clampRowSpans(group *html.Node) {
	var rows []*html.Node
	for row := group.FirstChild; row != nil; row = row.NextSibling {
		if row.Type == html.ElementNode && row.Data == "tr" {
			rows = append(rows, row)
		}
	}
	for i, row := range rows {
		for cell := row.FirstChild; cell != nil; cell = cell.NextSibling {
			for j, attr := range cell.Attr {
				if attr.Key == "rowspan" {
					if n, err := strconv.Atoi(attr.Val); err == nil && (n == 0 || n > len(rows)-i) {
						cell.Attr[j].Val = strconv.Itoa(len(rows) - i)
					}
				}
			}
		}
	}
}

// CSV 对齐 WeKnora 的宽表规则：宽度取最长记录，短行补空列；不丢弃多出的单元格。
func csvMarkdown(source string, maxBytes int) (string, error) {
	reader := csv.NewReader(strings.NewReader(source))
	reader.LazyQuotes, reader.TrimLeadingSpace, reader.FieldsPerRecord = true, true, -1
	records, err := reader.ReadAll()
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}
	width := 0
	for _, row := range records {
		width = max(width, len(row))
	}
	var out strings.Builder
	escape := strings.NewReplacer("\\", "\\\\", "|", "\\|", "\n", "<br>", "\r", "")
	for i, row := range records {
		out.WriteString("| ")
		for col := range width {
			cell := ""
			if col < len(row) {
				cell = row[col]
			}
			cell = escape.Replace(cell)
			out.WriteString(cell)
			out.WriteString(" |")
			if col+1 < width {
				out.WriteByte(' ')
			}
		}
		out.WriteByte('\n')
		if i == 0 {
			out.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
		}
		if out.Len() > maxBytes {
			return "", ErrResourceLimit
		}
	}
	return out.String(), nil
}
