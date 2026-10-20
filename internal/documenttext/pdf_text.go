// PDF 文本清洗与版面规则参考 Tencent/WeKnora，固定版本 2c0d8384398ca756ee414eef01b71caf91e2caa6。
// Copyright (C) 2026 Tencent. All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
package documenttext

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var pdfPageNumber = regexp.MustCompile(`^\p{Nd}{1,3}$`)
var pdfArxivPrefix = regexp.MustCompile(`^arXiv:\s*\S+`)
var pdfArxiv = regexp.MustCompile(`\s*arXiv:\s*\S+\s*(?:\[[^\]]+\])?\s*[^\n]*`)
var pdfFigureCaption = regexp.MustCompile(`^Figure\s+\p{Nd}+\b`)
var pdfFigureCaptionSearch = regexp.MustCompile(`\bFigure\s+\p{Nd}+\b`)
var pdfChartDebrisPattern = regexp.MustCompile(`(?i)^(?:[\p{Nd}\s.]+|\p{Nd}{1,2}|\p{Nd}+-layer|iter\.\s*\(1e4\)|(?:training|test)\s+error\s*\(%\))$`)
var pdfChartLayer = regexp.MustCompile(`(?i)^\p{Nd}+-layer$`)
var pdfTickLine = regexp.MustCompile(`^[\p{Nd}\s.()-]+$`)

// pdfPostprocess 只用于 PDF 文本层；Markdown、代码文档和电子表格不套用这些删除规则。
func pdfPostprocess(s string, cfg PDFOptions) string {
	if cfg.SanitizeText {
		s = strings.Map(func(r rune) rune {
			if r == '\u00ad' || (r >= '\u200b' && r <= '\u200f') || r == '\ufeff' || r == '\ufffe' || r == '\uffff' {
				return -1
			}
			return r
		}, s)
	}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if pdfPageNumber.MatchString(strings.TrimSpace(line)) || pdfArxivPrefix.MatchString(strings.TrimSpace(line)) {
			continue
		}
		if strings.Contains(line, "arXiv:") {
			line = strings.TrimSpace(pdfArxiv.ReplaceAllString(line, ""))
			if line == "" {
				continue
			}
		}
		if pdfFigureCaptionSearch.MatchString(line) {
			for len(kept) > 0 && pdfFigureInterior(kept[len(kept)-1]) {
				kept = kept[:len(kept)-1]
			}
		}
		kept = append(kept, line)
	}
	if !cfg.StripChartDebris {
		return strings.Join(kept, "\n")
	}
	var out []string
	for i := 0; i < len(kept); i++ {
		if pdfChartDebris(kept[i]) {
			j := i
			for j < len(kept) && (pdfChartDebris(kept[j]) || strings.TrimSpace(kept[j]) == "") {
				j++
			}
			if j-i >= 3 {
				i = j - 1
				continue
			}
		}
		out = append(out, kept[i])
	}
	return strings.Join(out, "\n")
}

func pdfChartDebris(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if pdfChartLayer.MatchString(s) || pdfChartDebrisPattern.MatchString(s) {
		return true
	}
	n := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			n++
		}
	}
	return utf8.RuneCountInString(s) <= 24 && n >= 3 && pdfTickLine.MatchString(s)
}

func pdfFigureInterior(s string) bool {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if s == "" || pdfFigureCaption.MatchString(s) {
		return false
	}
	if strings.HasPrefix(s, "arXiv:") || pdfPageNumber.MatchString(s) {
		return true
	}
	words := len(strings.Fields(s))
	if n >= 48 && words >= 8 {
		return false
	}
	if pdfChartDebris(s) {
		return true
	}
	if n >= 15 && strings.ContainsRune(".。!?！", []rune(s)[n-1]) {
		return false
	}
	return words < 7 && n <= 40
}

// pdfStripRepeatingLines 使用全文统计清理短页眉页脚；至少 4 个正文页、重复率 60%，不使用单篇文档关键词。
func pdfStripRepeatingLines(texts []string, scanned []bool) {
	count := 0
	frequency := map[string]int{}
	for i, s := range texts {
		if scanned[i] {
			continue
		}
		count++
		var lines []string
		for line := range strings.SplitSeq(s, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
		if len(lines) == 0 {
			continue
		}
		edges := map[string]bool{lines[0]: true, lines[len(lines)-1]: true}
		for edge := range edges {
			if utf8.RuneCountInString(edge) <= 80 {
				frequency[edge]++
			}
		}
	}
	if count < 4 {
		return
	}
	threshold := max(2, int(float64(count)*.6))
	for i, s := range texts {
		if scanned[i] {
			continue
		}
		var kept []string
		for line := range strings.SplitSeq(s, "\n") {
			if frequency[strings.TrimSpace(line)] < threshold {
				kept = append(kept, line)
			}
		}
		texts[i] = strings.Join(kept, "\n")
	}
}
