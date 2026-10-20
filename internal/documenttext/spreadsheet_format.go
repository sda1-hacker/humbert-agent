package documenttext

import (
	"archive/zip"
	"encoding/xml"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var percentColors = regexp.MustCompile(`(?i)\[(?:Black|Blue|Cyan|Green|Magenta|Red|White|Yellow|Color[0-9]+)\]`)
var percentPattern = regexp.MustCompile(`^(-?)(\()?(#,##)?0(?:\.(0+))?%(\))?$`)

// spreadsheetFormats 只补充 Tabula 未暴露的数字格式索引，不重复解析单元格正文。
func spreadsheetFormats(filename string) ([]string, error) {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	f, err := z.Open("xl/styles.xml")
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	var styles struct {
		Formats []struct {
			ID   int    `xml:"numFmtId,attr"`
			Code string `xml:"formatCode,attr"`
		} `xml:"numFmts>numFmt"`
		Cells []struct {
			ID int `xml:"numFmtId,attr"`
		} `xml:"cellXfs>xf"`
	}
	if err = xml.NewDecoder(f).Decode(&styles); err != nil {
		return nil, err
	}
	formats := map[int]string{9: "0%", 10: "0.00%"}
	for _, f := range styles.Formats {
		formats[f.ID] = f.Code
	}
	out := make([]string, len(styles.Cells))
	for i, cell := range styles.Cells {
		out[i] = formats[cell.ID]
	}
	return out, nil
}

// spreadsheetPercent 对齐 WeKnora 的显式百分比格式；转义符、条件格式和非标准装饰不猜测为百分数。
func spreadsheetPercent(raw, format string) string {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return ""
	}
	clean := percentColors.ReplaceAllString(format, "")
	if strings.ContainsAny(clean, "\"\\_*[]") {
		return ""
	}
	sections := strings.Split(clean, ";")
	if len(sections) > 4 {
		return ""
	}
	index := 0
	if value < 0 && len(sections) >= 2 {
		index = 1
	}
	if value == 0 && len(sections) >= 3 {
		index = 2
	}
	m := percentPattern.FindStringSubmatch(strings.TrimSpace(sections[index]))
	if m == nil || (m[2] == "") != (m[5] == "") {
		return ""
	}
	if index == 1 {
		value = math.Abs(value)
	}
	if len(m[4]) > 18 || math.IsInf(value*100, 0) {
		return ""
	}
	number := strconv.FormatFloat(value*100, 'f', len(m[4]), 64)
	if m[3] != "" {
		parts := strings.SplitN(number, ".", 2)
		integer := parts[0]
		sign := ""
		if strings.HasPrefix(integer, "-") {
			sign = "-"
			integer = integer[1:]
		}
		for i := len(integer) - 3; i > 0; i -= 3 {
			integer = integer[:i] + "," + integer[i:]
		}
		number = sign + integer
		if len(parts) == 2 {
			number += "." + parts[1]
		}
	}
	return m[1] + m[2] + number + "%" + m[5]
}
