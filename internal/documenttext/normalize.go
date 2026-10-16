package documenttext

import (
	"crypto/rand"
	"encoding/csv"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/tsawler/tabula"
	tabularag "github.com/tsawler/tabula/rag"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var htmlRowStart = regexp.MustCompile(`\s*<tr\b`)

// convertHTML 让 Tabula 转换正文，并在转换前保留表格片段，防止库先把合并关系和单元格引用压平。
func convertHTML(path string, md tabularag.MarkdownOptions, opts ParserOptions) (string, []tabula.Warning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	source := string(data)
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
	var out, plain strings.Builder
	var fence Fence
	flush := func() { out.WriteString(normalizeTables(plain.String())); plain.Reset() }
	for line := range strings.SplitAfterSeq(text, "\n") {
		active := fence.Active()
		boundary := fence.Observe(line)
		if !active && boundary {
			flush()
			out.WriteString(line)
		} else if active {
			out.WriteString(line)
		} else {
			plain.WriteString(line)
		}
	}
	flush()
	return out.String()
}

// Fence 跟踪 Markdown 围栏；四个反引号中的三个反引号不会被误认为结束，波浪线围栏同样受保护。
type Fence struct{ marker string }

func (f *Fence) Active() bool   { return f.marker != "" }
func (f *Fence) Marker() string { return f.marker }
func (f *Fence) Observe(line string) bool {
	line = strings.TrimSpace(line)
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return false
	}
	n := 0
	for n < len(line) && line[n] == line[0] {
		n++
	}
	if n < 3 {
		return false
	}
	if f.marker == "" {
		f.marker = line[:n]
		return true
	}
	if line[0] == f.marker[0] && n >= len(f.marker) && strings.TrimSpace(line[n:]) == "" {
		f.marker = ""
		return true
	}
	return false
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

// CSV 逐条读取，保留单元格中的引号、逗号和换行；显式拒绝不一致的列数，而不是带错位数据继续建索引。
func csvMarkdown(source string, maxBytes int) (string, error) {
	reader := csv.NewReader(strings.NewReader(source))
	var out strings.Builder
	for row := 0; ; row++ {
		cells, err := reader.Read()
		if err == io.EOF {
			return out.String(), nil
		}
		if err != nil {
			return "", err
		}
		out.WriteByte('|')
		for _, cell := range cells {
			cell = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(cell, "\\", "\\\\"), "|", "\\|"), "\n", "<br>")
			out.WriteString(" " + cell + " |")
		}
		out.WriteByte('\n')
		if row == 0 {
			out.WriteString("|" + strings.Repeat(" --- |", len(cells)) + "\n")
		}
		if out.Len() > maxBytes {
			return "", ErrResourceLimit
		}
	}
}
