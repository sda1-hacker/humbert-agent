package documenttext

import (
	"bytes"
	"context"

	"github.com/gohugoio/hugo-goldmark-extensions/passthrough"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	tableast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// MarkdownRange 是原始 Markdown 的字节区间，不包含渲染时补充的内容。
type MarkdownRange struct{ Start, End int }

// Markdown 保存 Goldmark AST 与来源范围；转换清洗和 RAG 共用同一套语法。
type Markdown struct {
	Source []byte
	Root   ast.Node
	ranges map[ast.Node]MarkdownRange
}

type markdownCanceled struct{ err error }

// ParseMarkdown 只解析结构，不生成 HTML，也不访问链接、图片或本地文件。
// 使用 v1 是为了直接复用 Hugo 的公式扩展，避免自行实现公式定界符解析。
func ParseMarkdown(ctx context.Context, source string) (result *Markdown, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Goldmark 的解析接口不返回 error；仅在自己的回调中用私有信号中止取消，其他 panic 原样抛出。
	defer func() {
		if value := recover(); value != nil {
			if canceled, ok := value.(markdownCanceled); ok {
				result, err = nil, canceled.err
			} else {
				panic(value)
			}
		}
	}()
	m := &Markdown{Source: []byte(source), ranges: make(map[ast.Node]MarkdownRange)}
	p := goldmark.New(goldmark.WithExtensions(extension.GFM, passthrough.New(passthrough.Config{
		BlockDelimiters:  []passthrough.Delimiters{{Open: "$$", Close: "$$"}, {Open: `\[`, Close: `\]`}},
		InlineDelimiters: []passthrough.Delimiters{{Open: "$", Close: "$"}, {Open: `\(`, Close: `\)`}},
	}))).Parser()
	p.AddOptions(markdownTracking{ctx: ctx, markdown: m})
	m.Root = p.Parse(text.NewReader(m.Source))
	// 变换后的表格、Setext 标题等节点也从 AST 的来源片段恢复范围，不通过文字搜索定位。
	err = ast.Walk(m.Root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering {
			m.indexRange(n)
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	return m, ctx.Err()
}

// Range 返回包含 Markdown 标记的来源范围；块节点的边界覆盖完整原始行。
func (m *Markdown) Range(n ast.Node) MarkdownRange { return m.ranges[n] }

func (m *Markdown) indexRange(n ast.Node) {
	r, exists := m.ranges[n]
	if !exists {
		r.Start, r.End = len(m.Source), 0
	}
	include := func(start, end int) {
		if start >= 0 && end >= start && end <= len(m.Source) {
			r.Start, r.End = min(r.Start, start), max(r.End, end)
		}
	}
	if pos := n.Pos(); pos >= 0 {
		include(pos, pos)
	}
	if n.Type() != ast.TypeInline {
		for i := range n.Lines().Len() {
			s := n.Lines().At(i)
			include(s.Start, s.Stop)
		}
	}
	switch node := n.(type) {
	case *ast.Text:
		include(node.Segment.Start, node.Segment.Stop)
	case *passthrough.PassthroughInline:
		include(node.Segment.Start, node.Segment.Stop)
	case *ast.RawHTML:
		for i := range node.Segments.Len() {
			s := node.Segments.At(i)
			include(s.Start, s.Stop)
		}
	case *ast.HTMLBlock:
		if node.HasClosure() {
			include(node.ClosureLine.Start, node.ClosureLine.Stop)
		}
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if child, ok := m.ranges[c]; ok {
			include(child.Start, child.End)
		}
	}
	if _, table := n.(*tableast.Table); table && n.FirstChild() != nil {
		header := m.ranges[n.FirstChild()]
		r.Start = header.Start
		// 表格分隔行不生成独立 AST 节点；空表也必须保留这条原文行。
		end := len(m.Source)
		if next := bytes.IndexByte(m.Source[header.End:], '\n'); next >= 0 {
			end = header.End + next + 1
		}
		r.End = max(r.End, end)
	}
	if r.Start > r.End {
		return // 没有来源的合成节点不生成虚假的坐标。
	}
	if n.Type() != ast.TypeInline && n.Type() != ast.TypeDocument {
		r.Start = bytes.LastIndexByte(m.Source[:r.Start], '\n') + 1
		if r.End == r.Start || (r.End > 0 && m.Source[r.End-1] != '\n') {
			if next := bytes.IndexByte(m.Source[r.End:], '\n'); next >= 0 {
				r.End += next + 1
			} else {
				r.End = len(m.Source)
			}
		}
	}
	m.ranges[n] = r
}

// markdownTracking 只记录第三方解析器消费的位置和检查取消，不判断任何 Markdown 语法。
type markdownTracking struct {
	ctx      context.Context
	markdown *Markdown
}

func (t markdownTracking) check() {
	if err := t.ctx.Err(); err != nil {
		panic(markdownCanceled{err})
	}
}

func (t markdownTracking) SetParserOption(c *parser.Config) {
	for i := range c.BlockParsers {
		c.BlockParsers[i].Value = &trackedBlock{BlockParser: c.BlockParsers[i].Value.(parser.BlockParser), markdownTracking: t}
	}
	for i := range c.InlineParsers {
		c.InlineParsers[i].Value = &trackedInline{InlineParser: c.InlineParsers[i].Value.(parser.InlineParser), markdownTracking: t}
	}
}

type trackedBlock struct {
	parser.BlockParser
	markdownTracking
}

func (p *trackedBlock) Open(parent ast.Node, r text.Reader, pc parser.Context) (ast.Node, parser.State) {
	p.check()
	_, before := r.PeekLine()
	n, state := p.BlockParser.Open(parent, r, pc)
	if n != nil {
		p.markdown.ranges[n] = MarkdownRange{before.Start, before.Stop}
	}
	return n, state
}

func (p *trackedBlock) Continue(n ast.Node, r text.Reader, pc parser.Context) parser.State {
	p.check()
	_, before := r.PeekLine()
	state := p.BlockParser.Continue(n, r, pc)
	_, after := r.Position()
	if state&parser.Continue != 0 || after.Start > before.Start {
		location := p.markdown.ranges[n]
		location.End = max(location.End, before.Stop)
		p.markdown.ranges[n] = location
	}
	return state
}

func (p *trackedBlock) Close(n ast.Node, r text.Reader, pc parser.Context) {
	p.check()
	p.BlockParser.Close(n, r, pc)
}

func (p *trackedBlock) SetOption(name parser.OptionName, value any) {
	if setter, ok := p.BlockParser.(parser.SetOptioner); ok {
		setter.SetOption(name, value)
	}
}

type trackedInline struct {
	parser.InlineParser
	markdownTracking
}

func (p *trackedInline) Parse(parent ast.Node, r text.Reader, pc parser.Context) ast.Node {
	p.check()
	_, before := r.Position()
	n := p.InlineParser.Parse(parent, r, pc)
	if n != nil {
		_, after := r.Position()
		start := before.Start
		if n.Pos() >= 0 {
			start = min(start, n.Pos()) // 链接解析在右括号处结束，但起点由 Goldmark 保存。
		}
		p.markdown.ranges[n] = MarkdownRange{start, after.Start}
	}
	return n
}

func (p *trackedInline) CloseBlock(parent ast.Node, r text.Reader, pc parser.Context) {
	p.check()
	if closer, ok := p.InlineParser.(parser.CloseBlocker); ok {
		closer.CloseBlock(parent, r, pc)
	}
}

func (p *trackedInline) SetOption(name parser.OptionName, value any) {
	if setter, ok := p.InlineParser.(parser.SetOptioner); ok {
		setter.SetOption(name, value)
	}
}

// HasIndexableText 复用 Goldmark AST 判断图片占位块，图片 alt/文件名不算正文。
// 代码、公式和 HTML 块仍按有效内容保留，避免只含结构内容的文档被错误丢弃。
func HasIndexableText(ctx context.Context, source string) (bool, error) {
	md, err := ParseMarkdown(ctx, source)
	if err != nil {
		return false, err
	}
	found := false
	err = ast.Walk(md.Root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Image:
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			found = len(bytes.TrimSpace(node.Segment.Value(md.Source))) > 0
		case *ast.String:
			found = len(bytes.TrimSpace(node.Value)) > 0
		case *ast.CodeBlock, *ast.FencedCodeBlock:
			found = len(bytes.TrimSpace(n.Lines().Value(md.Source))) > 0
		case *passthrough.PassthroughInline, *passthrough.PassthroughBlock, *ast.HTMLBlock, *ast.RawHTML:
			found = true
		}
		if found {
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return found, err
}
