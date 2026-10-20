package documenttext

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/tsawler/tabula/xlsx"
)

var spreadsheetImageFormula = regexp.MustCompile(`(?i)^=?(?:_xlfn\.)?(?:DISPIMG|IMAGE)\(`)

// convertSpreadsheet 对齐 WeKnora ExcelParser：按工作表/行输出列标签和值，首行默认保留。
// 合并单元格的值展开到覆盖范围，空行跳过；这里不生成检索块，统一由下游 Go chunker 处理。
func convertSpreadsheet(filename string, opts ParserOptions) (string, []sourceFragment, error) {
	r, err := xlsx.Open(filename)
	if err != nil {
		return "", nil, err
	}
	defer r.Close()
	var formats []string
	if opts.XLSXPercentAsText {
		formats, err = spreadsheetFormats(filename)
		if err != nil {
			return "", nil, err
		}
	}
	var out strings.Builder
	var fragments []sourceFragment
	for i := 0; i < r.SheetCount(); i++ {
		sheet, err := r.Sheet(i)
		if err != nil {
			return "", nil, err
		}
		if len(sheet.Rows) == 0 {
			continue
		}
		rows, cols := len(sheet.Rows), 0
		for _, row := range sheet.Rows {
			cols = max(cols, len(row))
		}
		if cols == 0 {
			continue
		}
		// 合法的合并区域可以跨过原有非空单元格边界；仅展开逻辑范围，不分配另一份稠密表格。
		limit := spreadsheetCellLimit(opts)
		for _, region := range sheet.MergedRegions {
			if region.StartRow < 0 || region.StartCol < 0 || region.EndRow < region.StartRow || region.EndCol < region.StartCol {
				continue
			}
			if region.EndRow >= limit || region.EndCol >= limit {
				return "", nil, ErrResourceLimit
			}
			if cell := sheet.Cell(region.StartRow, region.StartCol); cell != nil && !cell.IsEmpty() {
				rows = max(rows, region.EndRow+1)
				cols = max(cols, region.EndCol+1)
			}
		}
		if cols > limit || rows > limit/cols {
			return "", nil, ErrResourceLimit
		}
		merged := map[[2]int]*xlsx.Cell{}
		for _, region := range sheet.MergedRegions {
			cell := sheet.Cell(region.StartRow, region.StartCol)
			if cell == nil || cell.IsEmpty() || region.StartRow < 0 || region.StartCol < 0 {
				continue
			}
			for row := region.StartRow; row <= region.EndRow; row++ {
				for col := region.StartCol; col <= region.EndCol; col++ {
					merged[[2]int{row, col}] = cell
				}
			}
		}
		value := func(row, col int) string {
			cell := sheet.Cell(row, col)
			if root := merged[[2]int{row, col}]; root != nil {
				cell = root
			}
			if cell == nil || cell.IsEmpty() || spreadsheetImageFormula.MatchString(cell.Value) || spreadsheetImageFormula.MatchString(cell.Formula) {
				return ""
			}
			if cell.Type == xlsx.CellTypeNumber && cell.RawValue != "" {
				if opts.XLSXPercentAsText && cell.StyleIndex >= 0 && cell.StyleIndex < len(formats) {
					if displayed := spreadsheetPercent(cell.RawValue, formats[cell.StyleIndex]); displayed != "" {
						return displayed
					}
				}
				return cell.RawValue
			}
			return cell.Value
		}
		labels := make([]string, cols)
		used := map[string]int{}
		start := 0
		if opts.XLSXFirstRowAsHeader && rows >= 2 {
			start = 1
		}
		for col := range cols {
			label := ""
			if start == 1 {
				label = strings.TrimSpace(value(0, col))
			}
			if label == "" {
				label = xlsx.IndexToColumn(col)
			}
			used[label]++
			if used[label] > 1 {
				labels[col] = fmt.Sprintf("%s_%d", label, used[label])
			} else {
				labels[col] = label
			}
		}
		for row := start; row < rows; row++ {
			var cells []string
			for col := range cols {
				if v := value(row, col); v != "" {
					cells = append(cells, labels[col]+": "+v)
				}
			}
			if len(cells) == 0 {
				continue
			}
			line := strings.Join(cells, ",") + "\n"
			if len(line) > opts.MaxOutputBytes-out.Len() {
				return "", nil, ErrResourceLimit
			}
			out.WriteString(line)
			if len(fragments) < maxSourceBlocks {
				fragments = append(fragments, sourceFragment{line, SourceLocator{Type: "sheet", Sheet: sheet.Name, RowStart: row + 1, RowEnd: row + 1}})
			}
		}
	}
	return out.String(), fragments, nil
}

// spreadsheetCellLimit 复用解压预算估算矩阵内存，128 字节按 Tabula Cell 的字段大小留余量。
func spreadsheetCellLimit(opts ParserOptions) int {
	opts = opts.effective()
	return max(1, int(min(uint64(opts.MaxOutputBytes), opts.MaxExpandedBytes/128)))
}

// preflightSpreadsheet 在第三方库分配矩阵之前检查实际行、单元格和合并坐标；不信任 dimension 声明。
func preflightSpreadsheet(file *zip.File, opts ParserOptions) error {
	input, err := file.Open()
	if err != nil {
		return err
	}
	defer input.Close()
	decoder := xml.NewDecoder(io.LimitReader(input, int64(min(opts.MaxExpandedBytes, uint64(^uint64(0)>>1)))))
	limit := spreadsheetCellLimit(opts)
	rows, cols := 1, 1
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		for _, attr := range start.Attr {
			r, c := 0, 0
			switch {
			case start.Name.Local == "row" && attr.Name.Local == "r":
				n, e := strconv.Atoi(attr.Value)
				if e != nil || n < 1 {
					continue
				}
				if n > limit {
					return fmt.Errorf("%w: Excel row count", ErrResourceLimit)
				}
				r = n - 1
			case start.Name.Local == "c" && attr.Name.Local == "r":
				c, r, err = xlsx.ParseCellRef(attr.Value)
				if err != nil {
					continue
				}
			case start.Name.Local == "mergeCell" && attr.Name.Local == "ref":
				_, _, c, r, err = xlsx.ParseRangeRef(attr.Value)
				if err != nil {
					continue
				}
			default:
				continue
			}
			if r >= limit || c >= limit {
				return fmt.Errorf("%w: Excel cell range", ErrResourceLimit)
			}
			rows, cols = max(rows, r+1), max(cols, c+1)
			if cols > limit || rows > limit/cols {
				return fmt.Errorf("%w: Excel cell grid", ErrResourceLimit)
			}
		}
	}
}
