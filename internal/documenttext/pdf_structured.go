package documenttext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/tsawler/tabula/reader"
	"github.com/yuin/goldmark/ast"
	tableast "github.com/yuin/goldmark/extension/ast"
	"golang.org/x/net/html"
)

// paddlePage 只解码 pipeline 实际提供的 Markdown 与内嵌图片；不访问返回的图片 URL 或本地路径。
type paddlePage struct {
	Markdown struct {
		Text   string            `json:"text"`
		Images map[string]string `json:"images"`
	} `json:"markdown"`
}

// convertPaddlePDF 对齐 WeKnora 737d237 的自托管 PaddleOCR-VL 请求协议与结构还原参数。
// 在现有解析子进程内执行；父 context 取消会终止子进程和 HTTP 连接，不启动后台任务。
// 默认仍用 Tabula；显式选择的结构化引擎失败时返回错误，不能静默降级成结构已丢失的文字。
func convertPaddlePDF(filename string, opts ParserOptions) (Result, error) {
	pdf, err := reader.Open(filename)
	if err != nil {
		return Result{}, err
	}
	pageCount, err := pdf.PageCount()
	pdf.Close()
	if err != nil || pageCount <= 0 || pageCount > maxSourceBlocks {
		return Result{}, errors.New("document parser: invalid source PDF page count")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return Result{}, err
	}
	if int64(len(data)) > opts.MaxInputBytes {
		return Result{}, ErrResourceLimit
	}
	payload := map[string]any{
		"file": base64.StdEncoding.EncodeToString(data), "fileType": 0, "visualize": false,
		"markdownIgnoreLabels":      []string{"header", "header_image", "footer", "footer_image", "number", "footnote", "aside_text"},
		"useDocOrientationClassify": false, "useDocUnwarping": false, "useLayoutDetection": true,
		"useChartRecognition": false, "useSealRecognition": true, "useOcrForImageBlock": false,
		"mergeTables": true, "relevelTitles": true, "restructurePages": true,
		"layoutShapeMode": "auto", "promptLabel": "ocr", "layoutNms": true,
		"repetitionPenalty": 1, "temperature": 0, "topP": 1, "minPixels": 147384, "maxPixels": 2822400,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	endpoint := strings.TrimRight(opts.pdfOptions().PaddleOCRVLEndpoint, "/") + "/layout-parsing"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, errors.New("document parser: invalid PaddleOCR-VL request")
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: opts.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("document parser: PaddleOCR-VL request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("document parser: PaddleOCR-VL HTTP %d", resp.StatusCode) // 不回显服务端正文或文件数据。
	}
	var response struct {
		ErrorCode int `json:"errorCode"`
		Result    struct {
			Pages []paddlePage `json:"layoutParsingResults"`
		} `json:"result"`
	}
	// Base64、JSON 转义及来源元数据也有硬上限，解码后的正文和图片还会单独检查。
	encoded, err := io.ReadAll(io.LimitReader(resp.Body, int64(parserResponseLimit(opts))+1))
	if err != nil {
		return Result{}, err
	}
	if len(encoded) > parserResponseLimit(opts) {
		return Result{}, ErrResourceLimit
	}
	if !utf8.Valid(encoded) {
		return Result{}, errors.New("document parser: invalid PaddleOCR-VL response encoding")
	}
	if err = json.Unmarshal(encoded, &response); err != nil {
		return Result{}, errors.New("document parser: invalid PaddleOCR-VL JSON response")
	}
	if response.ErrorCode != 0 {
		return Result{}, fmt.Errorf("document parser: PaddleOCR-VL error code %d", response.ErrorCode)
	}
	pages := response.Result.Pages
	if len(pages) != pageCount {
		return Result{}, fmt.Errorf("document parser: PaddleOCR-VL returned %d pages for %d source pages", len(pages), pageCount)
	}
	result := Result{Metadata: map[string]any{"parser": "paddleocr_vl", "parser_flow": "weknora-737d237", "page_count": len(pages)}}
	var markdown strings.Builder
	var fragments []sourceFragment
	imageBytes := 0
	for i, page := range pages {
		part := normalizeMarkdown(page.Markdown.Text)
		part, assets, err := paddleImages(part, page.Markdown.Images, i+1, len(result.Images), imageBytes, opts)
		if err != nil {
			return Result{}, err
		}
		for _, asset := range assets {
			imageBytes += len(asset.Data)
		}
		result.Images = append(result.Images, assets...)
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if len(part)+markdown.Len()+imageBytes+2 > opts.MaxOutputBytes {
			return Result{}, ErrResourceLimit
		}
		if markdown.Len() > 0 {
			markdown.WriteString("\n\n")
		}
		markdown.WriteString(part)
		// 跨页重组可能把后续页的行移到前页。表格没有逐行来源证据时保留整篇页码范围，不能伪造单页定位。
		md, err := ParseMarkdown(ctx, part)
		if err != nil {
			return Result{}, err
		}
		for node := md.Root.FirstChild(); node != nil; node = node.NextSibling() {
			r := md.Range(node)
			loc := SourceLocator{Type: "pdf", Page: i + 1, Mapping: "normalized_markdown"}
			_, table := node.(*tableast.Table)
			if table || len(HTMLTableRanges(part[r.Start:r.End])) > 0 {
				loc.Page, loc.PageEnd, loc.Partial = 1, len(pages), len(pages) > 1
			}
			fragments = append(fragments, sourceFragment{part[r.Start:r.End], loc})
			if len(fragments) > maxSourceBlocks {
				return Result{}, ErrResourceLimit
			}
		}
	}
	result.Markdown = markdown.String()
	if strings.TrimSpace(result.Markdown) == "" {
		return Result{}, errors.New("document parser: PaddleOCR-VL returned no text")
	}
	result.SourceBlocks = alignSourceBlocks(result.Markdown, fragments)
	result.Metadata["native_text_bytes"] = len(result.Markdown)
	return checkedResult(result, opts)
}

// paddleImages 仅改写 AST 确认的图片节点，代码示例不受影响；同名图片按页隔离，原编码校验后保存。
func paddleImages(text string, images map[string]string, page, count, used int, opts ParserOptions) (string, []ImageReference, error) {
	md, err := ParseMarkdown(context.Background(), text)
	if err != nil {
		return "", nil, err
	}
	var out strings.Builder
	var assets []ImageReference
	resolved := make(map[string]string)
	stored := make(map[string]bool)
	at := 0
	resolve := func(destination string) (string, error) {
		if !opts.PreserveImages {
			return "", nil
		}
		if ref, ok := resolved[destination]; ok {
			return ref, nil
		}
		encoded, ok := images[destination]
		if strings.HasPrefix(destination, "data:image/") {
			encoded, ok = destination, true
		}
		if !ok {
			return "", errors.New("document parser: PaddleOCR-VL image data missing; remote/local image paths are not fetched")
		}
		if strings.HasPrefix(encoded, "data:") {
			_, encoded, _ = strings.Cut(encoded, ",")
		}
		if base64.StdEncoding.DecodedLen(len(encoded)) > opts.MaxOutputBytes-used {
			return "", ErrResourceLimit
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", errors.New("document parser: invalid PaddleOCR-VL image data")
		}
		mime, ext, err := ImageInfo(data, opts.MaxImagePixels)
		if err != nil {
			return "", err
		}
		name := fmt.Sprintf("page-%d-%x%s", page, sha256.Sum256(data), ext)
		ref := "images/" + name
		if !stored[ref] {
			if count+len(assets) >= opts.MaxImages {
				return "", ErrResourceLimit
			}
			used += len(data)
			assets = append(assets, ImageReference{OriginalRef: ref, Filename: name, MIME: mime, Data: data, Page: page, Locator: SourceLocator{Type: "pdf", Page: page, Mapping: "normalized_markdown", Image: ref}})
			stored[ref] = true
		}
		resolved[destination] = ref
		return ref, nil
	}
	write := func(start, end int, replacement string) error {
		if start < at || end < start || end > len(text) {
			return errors.New("document parser: invalid structured image range")
		}
		out.WriteString(text[at:start])
		out.WriteString(replacement)
		at = end
		return nil
	}
	err = ast.Walk(md.Root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		r := md.Range(node)
		switch node := node.(type) {
		case *ast.Image:
			ref, err := resolve(string(node.Destination))
			if err != nil {
				return ast.WalkStop, err
			}
			value := ""
			if ref != "" {
				value = "![文档图片](" + ref + ")"
			}
			return ast.WalkSkipChildren, write(r.Start, r.End, value)
		case *ast.HTMLBlock, *ast.RawHTML:
			// 复杂表格里的 img 同样保存原图；HTML tokenizer 定位标签，不能直接全局替换图片路径。
			tokens := html.NewTokenizer(strings.NewReader(text[r.Start:r.End]))
			offset := r.Start
			for {
				kind := tokens.Next()
				start := offset
				offset += len(tokens.Raw())
				if kind == html.ErrorToken {
					break
				}
				if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
					continue
				}
				token := tokens.Token()
				if token.Data != "img" {
					continue
				}
				value := ""
				for i, attr := range token.Attr {
					if attr.Key != "src" {
						continue
					}
					ref, err := resolve(attr.Val)
					if err != nil {
						return ast.WalkStop, err
					}
					if ref != "" {
						token.Attr[i].Val = ref
						value = token.String()
					}
				}
				if err := write(start, offset, value); err != nil {
					return ast.WalkStop, err
				}
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return "", nil, err
	}
	out.WriteString(text[at:])
	if !utf8.ValidString(out.String()) {
		return "", nil, errors.New("document parser: invalid structured text encoding")
	}
	return out.String(), assets, nil
}
