package documenttext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tsawler/tabula"
	"github.com/tsawler/tabula/ocr"
	"github.com/tsawler/tabula/reader"
	"github.com/tsawler/tabula/text"
	xdraw "golang.org/x/image/draw"
)

// ImageReference 对齐 WeKnora 的转换结果边界；Data 仅在进程/缓存间传递，不作为 Base64 正文入库。
type ImageReference struct {
	OriginalRef string
	Filename    string
	MIME        string
	Data        []byte
	Page        int
	Scanned     bool
	Locator     SourceLocator // Office 的段落/幻灯片/工作表，以及独立图片的来源。
}

type pdfFigure struct {
	image         ImageReference
	hash          string
	area          float64
	width, height int
}

// convertPDF 只转换与清洗，绝不调用 Tabula 的 RAG chunker；按页路由扫描页和文本页，最后统一进入 Go 分块器。
func convertPDF(filename string, opts ParserOptions) (Result, error) {
	cfg := opts.pdfOptions()
	if cfg.PaddleOCRVLEndpoint != "" {
		return convertPaddlePDF(filename, opts)
	}
	r, err := reader.Open(filename)
	if err != nil {
		return Result{}, err
	}
	defer r.Close()
	count, err := r.PageCount()
	if err != nil {
		return Result{}, err
	}
	if count <= 0 || count > maxSourceBlocks {
		return Result{}, fmt.Errorf("%w: invalid PDF page count %d", ErrResourceLimit, count)
	}
	texts, scanned := make([]string, count), make([]bool, count)
	result := Result{Metadata: map[string]any{"parser": "tabula", "parser_flow": "weknora-2c0d838", "page_count": count}}
	var figures []pdfFigure
	var renderer *pdfRenderer
	defer func() {
		if renderer != nil {
			renderer.Close()
		}
	}()
	scans, fallbacks, bytesUsed := 0, 0, 0
	for i := 0; i < count; i++ {
		page, err := r.GetPage(i)
		if err != nil {
			return Result{}, fmt.Errorf("PDF page %d: %w", i+1, err)
		}
		plain, err := r.ExtractText(page)
		if err != nil {
			return Result{}, fmt.Errorf("PDF page %d text: %w", i+1, err)
		}
		width, err := page.Width()
		if err != nil || width <= 0 || math.IsNaN(width) || math.IsInf(width, 0) {
			return Result{}, fmt.Errorf("PDF page %d: invalid width", i+1)
		}
		height, err := page.Height()
		if err != nil || height <= 0 || math.IsNaN(height) || math.IsInf(height, 0) {
			return Result{}, fmt.Errorf("PDF page %d: invalid height", i+1)
		}
		placements, err := r.ExtractPlacedImages(page)
		if err != nil {
			return Result{}, err
		}
		area := 0.
		for _, p := range placements {
			// 先检查放置记录中的像素尺寸，再让第三方库解压位图或渲染扫描页。
			if p.PixelWidth > 0 && p.PixelHeight > 0 && int64(p.PixelWidth) > opts.MaxImagePixels/int64(p.PixelHeight) {
				return Result{}, fmt.Errorf("%w: PDF image dimensions", ErrResourceLimit)
			}
			area += math.Abs(p.Width*p.Height) / (width * height)
		}
		scanned[i] = cfg.ForceScanned || area >= cfg.ScanImageRatio || (utf8.RuneCountInString(strings.TrimSpace(plain)) < cfg.ScanMinChars && area >= .1)
		if scanned[i] {
			scans++
			if renderer == nil {
				renderer, err = newPDFRenderer(filename, opts)
				if err != nil {
					return Result{}, err
				}
			}
			data, err := renderer.Render(i+1, width, height, cfg, opts)
			if err != nil {
				return Result{}, err
			}
			if !opts.PreserveImages {
				// 附件不能返回无人处理的图片占位符；未编译 OCR 时明确失败，避免部分文字被误认为完整正文。
				plain, err = recognizePage(data, opts.OCRLanguage)
				if err != nil {
					return Result{}, fmt.Errorf("PDF page %d requires OCR: %w", i+1, err)
				}
				if strings.TrimSpace(plain) == "" {
					return Result{}, fmt.Errorf("PDF page %d OCR returned no text", i+1)
				}
				result.Warnings = append(result.Warnings, tabula.Warning{Code: tabula.WarningOCRFallback, Message: fmt.Sprintf("PDF page %d used OCR", i+1)})
			} else {
				name := fmt.Sprintf("page-%d.jpg", i+1)
				ref := "images/" + name
				if len(result.Images) >= opts.MaxImages {
					return Result{}, ErrResourceLimit
				}
				result.Images = append(result.Images, ImageReference{OriginalRef: ref, Filename: name, MIME: "image/jpeg", Data: data, Page: i + 1, Scanned: true, Locator: SourceLocator{Type: "pdf", Page: i + 1}})
				plain = fmt.Sprintf("![%s](%s)", name, ref)
				bytesUsed += len(data)
			}
		} else {
			if cfg.LayoutOrdering && !pdfPlainWellFormed(plain) {
				fragments, extractErr := r.ExtractTextFragments(page)
				if extractErr == nil {
					// 仅剔除明确越过 CropBox 的片段；Tabula 未暴露透明/隐藏字形属性，不伪造完整过滤能力。
					box, boxErr := page.CropBox()
					visible := make([]text.TextFragment, 0, len(fragments))
					for _, f := range fragments {
						if boxErr != nil || (f.X+f.Width >= box[0] && f.X <= box[2] && f.Y+f.Height >= box[1] && f.Y <= box[3]) {
							visible = append(visible, f)
						}
					}
					layout := pdfLayoutText(visible, width, cfg)
					if !pdfPreferPlain(plain, layout) {
						plain = layout
					} else {
						fallbacks++
					}
				} else {
					fallbacks++
				}
			}
			plain = pdfPostprocess(plain, cfg)
			if cfg.ExtractEmbeddedImages && opts.PreserveImages {
				images, err := r.ExtractPageImages(page)
				if err != nil {
					result.Warnings = append(result.Warnings, tabula.Warning{Message: fmt.Sprintf("PDF page %d image extraction failed: %v", i+1, err)})
					images = nil
				}
				seen := map[string]bool{}
				for _, p := range placements {
					if seen[p.Name] || p.PixelWidth < cfg.EmbedMinPixels || p.PixelHeight < cfg.EmbedMinPixels || math.Abs(p.Width*p.Height)/(width*height) < cfg.EmbedMinAreaRatio {
						continue
					}
					seen[p.Name] = true
					for _, img := range images {
						if img.Name != p.Name {
							continue
						}
						if img.Width <= 0 || img.Height <= 0 || int64(img.Width) > opts.MaxImagePixels/int64(img.Height) {
							return Result{}, fmt.Errorf("%w: PDF image dimensions", ErrResourceLimit)
						}
						data, mime, ext, err := pdfImageAsset(img, opts)
						if err != nil {
							result.Warnings = append(result.Warnings, tabula.Warning{Message: fmt.Sprintf("PDF page %d image decoding failed: %v", i+1, err)})
							continue
						}
						if len(data) > opts.MaxOutputBytes-bytesUsed {
							return Result{}, ErrResourceLimit
						}
						bytesUsed += len(data)
						hash := fmt.Sprintf("%x", sha256.Sum256(data))
						name := fmt.Sprintf("page-%d-image-%s%s", i+1, hash[:12], ext)
						if len(figures)+len(result.Images) >= opts.MaxImages {
							return Result{}, ErrResourceLimit
						}
						figures = append(figures, pdfFigure{ImageReference{OriginalRef: "images/" + name, Filename: name, MIME: mime, Data: data, Page: i + 1, Locator: SourceLocator{Type: "pdf", Page: i + 1}}, hash, math.Abs(p.Width*p.Height) / (width * height), p.PixelWidth, p.PixelHeight})
						break
					}
				}
			}
		}
		texts[i] = plain
		bytesUsed += len(plain)
		if bytesUsed > opts.MaxOutputBytes {
			return Result{}, ErrResourceLimit
		}
	}
	if opts.ExcludeHeadersAndFooters {
		pdfStripRepeatingLines(texts, scanned)
	}
	selected := selectPDFFigures(figures, count-scans, cfg)
	result.Images = append(result.Images, selected...)
	var markdown strings.Builder
	var fragments []sourceFragment
	nativeBytes := 0
	for i, part := range texts {
		if !scanned[i] {
			nativeBytes += len(strings.TrimSpace(part))
		}
		if opts.IncludePageNumbers {
			part = fmt.Sprintf("<!-- Page %d -->\n\n%s", i+1, part)
		}
		for _, img := range selected {
			if img.Page == i+1 {
				part += "\n\n![" + img.Filename + "](" + img.OriginalRef + ")"
			}
		}
		part = strings.TrimSpace(normalizeMarkdown(part))
		if part == "" {
			continue
		}
		if markdown.Len() > 0 {
			markdown.WriteString("\n\n")
		}
		markdown.WriteString(part)
		fragments = append(fragments, sourceFragment{part, SourceLocator{Type: "pdf", Page: i + 1, Mapping: "exact"}})
	}
	result.Markdown = markdown.String()
	result.SourceBlocks = alignSourceBlocks(result.Markdown, fragments)
	result.Metadata["scanned_page_count"], result.Metadata["text_page_count"] = scans, count-scans
	result.Metadata["native_text_bytes"] = nativeBytes
	result.Metadata["layout_fallback_pages"], result.Metadata["embedded_image_count"] = fallbacks, len(selected)
	return result, nil
}

// pdfImageAsset 优先保留 PDF 中完整的 JPEG 数据，避免重新压缩损失细节和重复编码。
// 像素流没有独立文件编码时，才由 Tabula 无损导出 PNG；原始 PDF 始终另行保存。
func pdfImageAsset(img reader.PageImage, opts ParserOptions) ([]byte, string, string, error) {
	data := img.Data
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 || img.ColorSpace == "Separation" {
		var err error
		data, err = img.ToPNG()
		if err != nil {
			return nil, "", "", err
		}
	}
	mime, ext, err := ImageInfo(data, opts.MaxImagePixels)
	return data, mime, ext, err
}

func selectPDFFigures(figures []pdfFigure, textPages int, cfg PDFOptions) []ImageReference {
	pages := map[string]map[int]bool{}
	for _, f := range figures {
		if pages[f.hash] == nil {
			pages[f.hash] = map[int]bool{}
		}
		pages[f.hash][f.image.Page] = true
	}
	threshold := max(2, int(float64(textPages)*cfg.EmbedRepeatPageFraction))
	seen := map[string]bool{}
	var out []ImageReference
	for _, f := range figures {
		key := strconv.Itoa(f.image.Page) + ":" + f.hash
		if f.area < cfg.EmbedMinAreaRatio || f.width < cfg.EmbedMinPixels || f.height < cfg.EmbedMinPixels || len(pages[f.hash]) >= threshold || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f.image)
		if len(out) >= cfg.EmbedMaxImages {
			break
		}
	}
	return out
}

func pdfJPEG(data []byte, cfg PDFOptions) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	edge := max(b.Dx(), b.Dy())
	if cfg.RenderMaxEdge > 0 && edge > cfg.RenderMaxEdge {
		scale := float64(cfg.RenderMaxEdge) / float64(edge)
		scaled := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))))
		xdraw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), img, b, xdraw.Over, nil)
		img = scaled
	}
	var out bytes.Buffer
	err = jpeg.Encode(&out, img, &jpeg.Options{Quality: cfg.JPEGQuality})
	return out.Bytes(), err
}

func recognizePage(data []byte, language string) (string, error) {
	client, err := ocr.New()
	if err != nil {
		// 普通桌面构建无需链接 Tesseract CGO：安装命令行程序也能识别，worker 负责超时及进程树取消。
		binary, findErr := parserExecutable("tesseract", "")
		if findErr != nil {
			return "", fmt.Errorf("请安装 Tesseract 及对应语言包，或配置 Ollama/OpenAI 视觉模型: %w", err)
		}
		file, writeErr := os.CreateTemp("", "humbert-ocr-*")
		if writeErr != nil {
			return "", writeErr
		}
		defer os.Remove(file.Name())
		_, writeErr = file.Write(data)
		closeErr := file.Close()
		if writeErr != nil {
			return "", writeErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		args := []string{file.Name(), "stdout"}
		if language != "" {
			args = append(args, "-l", language)
		}
		command := exec.Command(binary, args...)
		output := &boundedWriter{limit: DefaultOptions().MaxOutputBytes}
		stderr := &boundedWriter{limit: 4096}
		command.Stdout, command.Stderr = output, stderr
		if err := command.Run(); err != nil {
			return "", fmt.Errorf("Tesseract 识别失败: %s: %w", strings.TrimSpace(stderr.String()), err)
		}
		if output.exceeded {
			return "", ErrResourceLimit
		}
		return output.String(), nil
	}
	defer client.Close()
	if language != "" {
		if err := client.SetLanguage(language); err != nil {
			return "", err
		}
	}
	return client.RecognizeImage(data)
}

// RecognizeImage 是 Loader 可替换的本地 OCR 适配器；没有编译 OCR 支持时返回明确错误。
func RecognizeImage(ctx context.Context, data []byte, _ string, language string) (string, error) {
	opts := DefaultOptions()
	opts.OCRLanguage = language
	if int64(len(data)) > opts.MaxInputBytes {
		return "", ErrResourceLimit
	}
	if _, _, err := ImageInfo(data, opts.MaxImagePixels); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	result, err := callWorker(ctx, workerRequest{Image: data, Options: opts})
	return result.Markdown, err
}
