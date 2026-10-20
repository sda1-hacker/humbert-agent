package documenttext

import (
	"context"
	"fmt"
	"image/jpeg"
	"io"
	"math"
	"os"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// pdfRenderer 在一次性解析进程内延迟创建，一篇 PDF 共用一个实例；纯文字 PDF 不启动渲染器。
// PDFium WASM 随 Go 二进制内嵌，文件通过内存传入，禁止使用库默认的整盘挂载。
type pdfRenderer struct {
	pool     pdfium.Pool
	instance pdfium.Pdfium
	document references.FPDF_DOCUMENT
	cancel   context.CancelFunc
}

func newPDFRenderer(filename string, opts ParserOptions) (*pdfRenderer, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	runtime := wazero.NewRuntimeConfig().WithCoreFeatures(api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling).
		WithMemoryLimitPages(8192).WithCloseOnContextDone(true) // WASM 线性内存上限 512 MiB，像素限制另行检查。
	pool, err := webassembly.Init(webassembly.Config{Context: ctx, MaxTotal: 1, FSConfig: wazero.NewFSConfig(), RuntimeConfig: runtime, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("PDFium 初始化失败: %w", err)
	}
	r := &pdfRenderer{pool: pool, cancel: cancel}
	r.instance, err = pool.GetInstanceWithContext(ctx)
	if err == nil {
		opened, openErr := r.instance.OpenDocument(&requests.OpenDocument{File: &data})
		err = openErr
		if err == nil {
			r.document = opened.Document
		}
	}
	if err != nil {
		r.Close()
		return nil, fmt.Errorf("PDFium 打开文档失败: %w", err)
	}
	return r, nil
}

func (r *pdfRenderer) Close() {
	if r.instance != nil {
		_ = r.instance.Close()
	}
	_ = r.pool.Close()
	r.cancel()
}

// Render 先按 DPI/最长边计算像素预算，再调用库渲染整页；取消由外层 worker 和 WASM context 双重控制。
func (r *pdfRenderer) Render(page int, width, height float64, cfg PDFOptions, opts ParserOptions) ([]byte, error) {
	w, h := width*float64(cfg.RenderDPI)/72, height*float64(cfg.RenderDPI)/72
	if cfg.RenderMaxEdge > 0 && math.Max(w, h) > float64(cfg.RenderMaxEdge) {
		scale := float64(cfg.RenderMaxEdge) / math.Max(w, h)
		w, h = w*scale, h*scale
	}
	if w > float64(opts.MaxImagePixels)/math.Max(1, h) {
		return nil, fmt.Errorf("%w: rendered PDF image dimensions", ErrResourceLimit)
	}
	rendered, err := r.instance.RenderPageInPixels(&requests.RenderPageInPixels{
		Page:  requests.Page{ByIndex: &requests.PageByIndex{Document: r.document, Index: page - 1}},
		Width: max(1, int(w)), Height: max(1, int(h)),
	})
	if err != nil {
		return nil, fmt.Errorf("PDF 第 %d 页渲染失败: %w", page, err)
	}
	defer rendered.Cleanup()
	out := &boundedWriter{limit: opts.MaxOutputBytes}
	if err := jpeg.Encode(out, rendered.Result.RenderedImage, &jpeg.Options{Quality: cfg.JPEGQuality}); err != nil {
		return nil, err
	}
	if out.exceeded {
		return nil, ErrResourceLimit
	}
	return out.Bytes(), nil
}
