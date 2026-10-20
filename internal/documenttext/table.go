package documenttext

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// HTMLTable 保存规范化表格的原文字节范围；跨行单元格连接的多行属于同一个不可分组。
// Markdown 仍由 Goldmark 解析，这里只借助标准 HTML tokenizer 记录 tr 和 rowspan。
type HTMLTable struct {
	Rows      []MarkdownRange
	HeaderEnd int
	Header    string
	Closing   string
}

// ParseHTMLTable 只拆分无嵌套且行组已规范化的表；不确定的结构整体保留，不能猜测合并关系。
func ParseHTMLTable(source string) (HTMLTable, bool) {
	type row struct {
		MarkdownRange
		span   int
		header bool
	}
	var rows []row
	tokenizer := html.NewTokenizer(strings.NewReader(source))
	offset, depth, rowStart, reach, cells := 0, 0, -1, 1, 0
	header := false
	for {
		kind := tokenizer.Next()
		start := offset
		offset += len(tokenizer.Raw())
		if kind == html.ErrorToken {
			break
		}
		if kind != html.StartTagToken && kind != html.EndTagToken {
			continue
		}
		token := tokenizer.Token()
		switch token.Data {
		case "table":
			if kind == html.StartTagToken {
				depth++
				if depth > 1 {
					return HTMLTable{}, false
				}
			} else {
				depth--
			}
		case "thead", "tbody", "tfoot":
			return HTMLTable{}, false // 未经过 normalizeTable 的输入暂按整体处理。
		case "tr":
			if depth != 1 {
				return HTMLTable{}, false
			}
			if kind == html.StartTagToken {
				if rowStart >= 0 {
					return HTMLTable{}, false
				}
				rowStart, reach, cells, header = start, 1, 0, true
			} else if rowStart >= 0 {
				if cells == 0 {
					return HTMLTable{}, false
				}
				rows = append(rows, row{MarkdownRange{rowStart, offset}, reach, header})
				rowStart = -1
			}
		case "td", "th":
			if kind != html.StartTagToken || rowStart < 0 {
				continue
			}
			cells++
			header = header && token.Data == "th" // 正文行的首列 th 是行标题，不能把整行误当作重复表头。
			for _, attr := range token.Attr {
				if attr.Key == "rowspan" {
					n, err := strconv.Atoi(attr.Val)
					if err != nil || n <= 0 || n > maxSourceBlocks {
						return HTMLTable{}, false
					}
					reach = max(reach, n)
				}
			}
		}
	}
	if depth != 0 || rowStart >= 0 || len(rows) == 0 {
		return HTMLTable{}, false
	}
	headerCount := 1
	if rows[0].header {
		for headerCount < len(rows) && rows[headerCount].header {
			headerCount++
		}
	}
	headerEnd := rows[headerCount-1].End
	if headerCount < len(rows) {
		headerEnd = rows[headerCount].Start
	}
	result := HTMLTable{HeaderEnd: headerEnd, Header: source[:headerEnd], Closing: source[rows[len(rows)-1].End:]}
	begin, until := 0, 0
	for i, r := range rows {
		until = max(until, i+r.span)
		if until > len(rows) {
			return HTMLTable{}, false
		}
		if i+1 < until {
			continue
		}
		if begin < headerCount && i+1 > headerCount {
			return HTMLTable{}, false // 表头跨入正文时不能把正文复制到所有续块。
		}
		start, end := rows[begin].Start, len(source)
		if begin == 0 {
			start = 0
		}
		if i+1 < len(rows) {
			end = rows[i+1].Start
		}
		result.Rows = append(result.Rows, MarkdownRange{Start: start, End: end})
		begin = i + 1
	}
	return result, true
}
