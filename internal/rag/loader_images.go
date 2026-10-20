package rag

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	einoOllama "github.com/cloudwego/eino-ext/components/model/ollama"
	einoOpenAI "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// storeImages 只保存真实图片文件并改写引用；OCR/VLM 在 Service 的图片阶段执行。
// 摘要文件名、原子写入和来源范围检查同时供独立图片、Office 插图和扫描页使用。
func (l *Loader) storeImages(ctx context.Context, result *documenttext.Result) ([]ImageAsset, error) {
	if len(result.Images) == 0 {
		return nil, nil
	}
	directory, err := l.assetDirectory()
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	assets := make([]ImageAsset, 0, len(result.Images))
	rewrites := make([]string, 0, len(result.Images)*2)
	imagePaths := map[string]string{}
	for _, img := range result.Images {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mime, ext, err := documenttext.ImageInfo(img.Data, l.config.ParseOptions.MaxImagePixels)
		if err != nil {
			return nil, err
		}
		if mime != img.MIME {
			return nil, errors.New("rag: parsed image MIME mismatch")
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(img.Data))
		name := hash + ext
		if err := saveImage(ctx, root, name, img.Data); err != nil {
			return nil, err
		}
		ref := "images/" + name
		locator := img.Locator
		locator.Image = ref
		locator.SourceHash = result.ContentHash
		assets = append(assets, ImageAsset{Path: name, Hash: hash, MIME: mime, Size: int64(len(img.Data)), Page: img.Page, Scanned: img.Scanned, Source: locator, Status: "pending"})
		rewrites = append(rewrites, "]("+img.OriginalRef+")", "]("+ref+")")
		// 结构化解析会在复杂表格内保留 img；图片落盘后 HTML 引用也必须使用同一个内容摘要路径。
		for _, quote := range []string{`"`, `'`} {
			rewrites = append(rewrites, "src="+quote+img.OriginalRef+quote, "src="+quote+ref+quote)
		}
		imagePaths[img.OriginalRef] = ref
	}
	if err := l.rewriteImageReferences(result, rewrites, imagePaths); err != nil {
		return nil, err
	}
	return assets, ctx.Err()
}

func (l *Loader) assetDirectory() (string, error) {
	directory := l.config.AssetDirectory
	if directory == "" || directory == "auto" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		directory = filepath.Join(base, "humbert-agent", "rag", "images")
	}
	return directory, nil
}

type imageTextFunc func(context.Context, []byte, string, string) (string, error)

func (l *Loader) imageHTTPClient() *http.Client {
	cfg := l.config.ImageRecognition.Effective()
	return &http.Client{Timeout: cfg.Timeout, Transport: imageResponseTransport{limit: int64(l.config.ParseOptions.OutputLimit())*6 + 65536},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// freezeImageModel 每次导入读取一次全局选择，后续动作固定模型与缓存身份；设置修改从下次导入生效。
func (l *Loader) freezeImageModel(ctx context.Context) (*Loader, error) {
	cfg := l.config.ImageRecognition.Effective()
	if cfg.Provider != ragconfig.ProviderAgent || l.config.ImageText != nil {
		return l, nil
	}
	if l.config.ImageModels == nil {
		return nil, errors.New("rag: provider=agent 需要注入应用 Model Registry")
	}
	instance, name, revision, err := l.config.ImageModels.ImageModel(ctx, l.imageHTTPClient(), cfg.MaxOutputTokens)
	if err != nil {
		return nil, err
	}
	if instance == nil || revision == "" || name == "" {
		return nil, errors.New("rag: 图片模型缺少实例、名称或稳定缓存指纹")
	}
	copy := *l
	copy.imageModel = instance
	copy.config.ImageRecognition = cfg
	copy.config.ImageRecognition.Model = name
	copy.config.ImageRecognition.APIKey = ""
	copy.config.ImageRecognition.Revision = cfg.Revision + ":" + revision
	return &copy, nil
}

// imageRecognizer 复用 Eino 的视觉模型适配，不自行维护 OpenAI/Ollama 协议。
func (l *Loader) imageRecognizer(ctx context.Context) (imageTextFunc, error) {
	if l.config.ImageText != nil {
		return l.config.ImageText, nil
	}
	cfg := l.config.ImageRecognition.Effective()
	if cfg.Provider == "tesseract" {
		return func(ctx context.Context, data []byte, mime, _ string) (string, error) {
			return documenttext.RecognizeImage(ctx, data, mime, l.config.ParseOptions.OCRLanguage)
		}, nil
	}
	client := l.imageHTTPClient()
	var chat model.BaseChatModel = l.imageModel
	var err error
	if cfg.Provider == ragconfig.ProviderAgent {
		if chat == nil {
			return nil, errors.New("rag: 全局图片模型尚未解析")
		}
	} else if cfg.Provider == ragconfig.ProviderOllama {
		chat, err = einoOllama.NewChatModel(ctx, &einoOllama.ChatModelConfig{BaseURL: cfg.BaseURL, Model: cfg.Model, HTTPClient: client, Options: &einoOllama.Options{NumPredict: cfg.MaxOutputTokens}})
	} else {
		chat, err = einoOpenAI.NewChatModel(ctx, &einoOpenAI.ChatModelConfig{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: cfg.Model, HTTPClient: client, MaxTokens: &cfg.MaxOutputTokens})
	}
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, data []byte, mime, prompt string) (string, error) {
		data, mime, err := documenttext.RecognitionImage(data, mime, l.config.ParseOptions)
		if err != nil {
			return "", err
		}
		encoded := base64.StdEncoding.EncodeToString(data) // 仅用于 HTTP 传输，资产仍是原始格式文件。
		response, err := chat.Generate(ctx, []*schema.Message{{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeText, Text: prompt},
			{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{Base64Data: &encoded, MIMEType: mime}, Detail: schema.ImageURLDetailHigh}},
		}}})
		if err != nil {
			return "", err
		}
		if response == nil {
			return "", errors.New("rag: empty image model response")
		}
		if len(response.ToolCalls) > 0 {
			return "", errors.New("rag: image recognition must not return tool calls")
		}
		if response.ResponseMeta != nil && (response.ResponseMeta.FinishReason == "length" || response.ResponseMeta.FinishReason == "max_tokens") {
			return "", errors.New("rag: 图片识别输出被截断，请增大 max_output_tokens 后重试")
		}
		return response.Content, nil
	}, nil
}

type imageResponseTransport struct{ limit int64 }

type imageHTTPError struct {
	status int
	wait   time.Duration
}

func (e *imageHTTPError) Error() string { return fmt.Sprintf("图片模型 HTTP %d", e.status) }
func (e *imageHTTPError) Timeout() bool { return e.status == 408 }
func (e *imageHTTPError) Temporary() bool {
	switch e.status {
	case 408, 429, 500, 502, 503, 504:
		return true
	}
	return false
}

func (t imageResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(req)
	if err == nil {
		if response.StatusCode >= 400 {
			wait := time.Duration(0)
			if value := response.Header.Get("Retry-After"); value != "" {
				if seconds, e := strconv.Atoi(value); e == nil && seconds >= 0 {
					wait = time.Duration(min(seconds, 86400)) * time.Second
				} else if at, e := http.ParseTime(value); e == nil {
					wait = max(0, time.Until(at))
				}
			}
			// 不把服务端可能回显的 Authorization、API Key 或原始图片写入错误/持久化状态。
			response.Body.Close()
			return nil, &imageHTTPError{status: response.StatusCode, wait: wait}
		}
		response.Body = http.MaxBytesReader(nil, response.Body, t.limit)
	}
	return response, err
}

// rewriteImageReferences 按原来源块替换引用并重算范围，图片路径变长后后续文字不会错位。
func (l *Loader) rewriteImageReferences(result *documenttext.Result, rewrites []string, imagePaths map[string]string) error {
	source := []rune(result.Markdown)
	replacer := strings.NewReplacer(rewrites...)
	var out strings.Builder
	offset, last := 0, 0
	blocks := make([]documenttext.SourceBlock, 0, len(result.SourceBlocks))
	write := func(text string) error {
		text = replacer.Replace(text)
		if len(text) > l.config.ParseOptions.OutputLimit()-out.Len() {
			return documenttext.ErrResourceLimit
		}
		out.WriteString(text)
		offset += utf8.RuneCountInString(text)
		return nil
	}
	for _, block := range result.SourceBlocks {
		end := block.End
		if block.Start < last || end > len(source) || end < block.Start {
			return errors.New("rag: invalid parser source range")
		}
		if err := write(string(source[last:block.Start])); err != nil {
			return err
		}
		start := block.Start
		block.Start = offset
		if err := write(string(source[start:end])); err != nil {
			return err
		}
		block.End = offset
		block.Locator.Start, block.Locator.End = block.Start, block.End
		if ref := imagePaths[block.Locator.Image]; ref != "" {
			block.Locator.Image = ref
		}
		blocks = append(blocks, block)
		last = end
	}
	if err := write(string(source[last:])); err != nil {
		return err
	}
	result.Markdown, result.SourceBlocks = out.String(), blocks
	return nil
}

func saveImage(ctx context.Context, root *os.Root, name string, data []byte) error {
	// 已有内容必须核验，防止缓存重试覆盖损坏文件；文件名仅由内容摘要生成。
	if err := verifyImage(root, name, data); err == nil {
		return ctx.Err()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	temp := ".image-" + rand.Text()
	defer root.Remove(temp)
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = root.Link(temp, name); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		return verifyImage(root, name, data)
	}
	return nil
}

// verifyImage 有界核验已有文件，损坏/超大文件不能通过复用路径占用无限内存。
func verifyImage(root *os.Root, name string, data []byte) error {
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != int64(len(data)) {
		return errors.New("rag: stored image size mismatch")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, io.LimitReader(file, int64(len(data))+1)); err != nil {
		return err
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != fmt.Sprintf("%x", sha256.Sum256(data)) {
		return errors.New("rag: stored image checksum mismatch")
	}
	return nil
}
