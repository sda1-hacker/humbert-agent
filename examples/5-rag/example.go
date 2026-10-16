// 本例分两步验证真实 RAG 流程：调用 Embedding 建立和查询向量，不调用回答模型。
// 需要 PostgreSQL 15+、pgvector 0.7+ 和 pg_search 0.25+。
// Embedding 支持 OpenAI 兼容服务或原生 Ollama，按 rag.example.yaml 配置。
//
// 在项目根目录填写 rag.example.yaml 的数据库与 Embedding 参数后运行；文件默认自动读取。
// HUMBERT_RAG_* 环境变量可以覆盖文件配置，不要求先设置环境变量。
//
//	go run ./cmd/rag-migrate -config rag.example.yaml
//	go run ./examples/5-rag ingest -config rag.example.yaml -file examples/5-rag/sample.md
//	go run ./examples/5-rag query -config rag.example.yaml -mode hybrid -query "取消导入后旧文档还能查询吗？"
//
// ingest 将原件保存到 source_directory，数据库保存文件引用、处理状态和父子分块，不保存 Markdown 全文。
// query 支持向量、BM25 和 RRF 混合检索，返回证据和上下文；精排遵循 Rerank 配置，默认关闭。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	ragembedding "github.com/sda1-hacker/humbert-agent/internal/rag/embedding"
	ragindexer "github.com/sda1-hacker/humbert-agent/internal/rag/indexer"
)

const usage = `RAG 入库与查询示例（调用 Embedding，不生成回答）：
  ingest -config rag.example.yaml -file /路径/文档.pdf [-document-id 稳定文档ID]
  retry  -config rag.example.yaml -document-id 稳定文档ID
  recover -config rag.example.yaml
  query  -config rag.example.yaml -query "查询问题" [-mode hybrid|semantic|keyword] [-limit 5]

默认读取项目根目录的 rag.example.yaml；database.name 默认 humbert_rag，database 下的账户需与实例一致。
Embedding 使用同一文件的 embedding 配置；OpenAI 兼容服务需地址/密钥，Ollama 需提前准备本地模型。
数据库要求 PostgreSQL 15+、pgvector 0.7+、pg_search 0.25+。
首次运行先执行 go run ./cmd/rag-migrate，自动创建缺失的数据库并初始化完整索引；建库需 CREATEDB 权限。
hybrid 为向量 + BM25 + RRF；semantic 只查向量；keyword 只查 BM25，不调用 Embedding。
导入与查询必须使用相同的 Embedding 模型、版本及输入规则，更换后使用新知识库重新导入。`

func main() {
	// Ctrl+C / SIGTERM 同时取消解析、模型与数据库请求，等待状态收尾后才关闭连接池。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// 第一次信号走正常取消与回写；重复信号恢复系统默认行为，避免异常组件阻止强制退出。
	go func() { <-ctx.Done(); stop() }()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, logging.SafeErrorText(err, 2048))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		_, err := fmt.Fprintln(out, usage)
		return err
	}
	switch args[0] {
	case "ingest":
		return runIngest(ctx, args[1:], out)
	case "retry":
		return runIngest(ctx, append([]string{"-retry"}, args[1:]...), out)
	case "recover":
		return runRecover(ctx, args[1:], out)
	case "query":
		return runQuery(ctx, args[1:], out)
	default:
		return fmt.Errorf("未知流程 %q，只支持 ingest、retry、recover 或 query", args[0])
	}
}

// loadConfig 复用集中配置，并在连接数据库或请求模型前检查模型名、PG 维度及候选规模。
func loadConfig(set *flag.FlagSet, args []string) (ragconfig.Config, error) {
	// 明确优先级：-config > HUMBERT_RAG_CONFIG > 项目根目录 rag.example.yaml。
	defaultPath := strings.TrimSpace(os.Getenv("HUMBERT_RAG_CONFIG"))
	if defaultPath == "" {
		defaultPath = "rag.example.yaml"
	}
	path := set.String("config", defaultPath, "RAG YAML 路径；默认读取项目根目录 rag.example.yaml，可用 HUMBERT_RAG_CONFIG 指定")
	if err := set.Parse(args); err != nil {
		return ragconfig.Config{}, err
	}
	if set.NArg() != 0 {
		return ragconfig.Config{}, errors.New("请使用命名参数，查看 ingest -h 或 query -h")
	}
	cfg, err := ragconfig.Load(*path)
	if err != nil {
		return ragconfig.Config{}, fmt.Errorf("加载 RAG 配置文件 %q 失败: %w", *path, err)
	}
	if err := cfg.ValidatePostgres(); err != nil {
		if errors.Is(err, ragconfig.ErrMissingModel) {
			return ragconfig.Config{}, fmt.Errorf("%w: 请填写配置文件 %q 的 embedding.model，或设置 HUMBERT_RAG_EMBEDDING_MODEL", err, *path)
		}
		return ragconfig.Config{}, err
	}
	return cfg, nil
}

// openPostgres 统一连接与模式检查；只有导入入口允许按 ensure_schema 显式建库、安装扩展和迁移。
// 查询入口始终只检查已有模式，不安装扩展、不改表；初始化失败立即关闭连接池。
func openPostgres(ctx context.Context, cfg ragconfig.Config, ingest bool) (*pgxpool.Pool, error) {
	dsn := cfg.Database.DSN()
	// 所有运行时组件都从唯一的 embedding.dimensions 取值，不另设存储维度配置。
	cfg.Postgres.Dimensions = cfg.Embedding.Dimensions
	if ingest && cfg.EnsureSchema {
		if err := ragindexer.EnsureDatabase(ctx, dsn, cfg.Postgres); err != nil {
			return nil, err
		}
		if err := ragindexer.EnsureExtensions(ctx, dsn, cfg.Postgres); err != nil {
			return nil, err
		}
	}
	pool, err := ragindexer.NewPool(ctx, dsn, cfg.Postgres)
	if err != nil {
		return nil, err
	}
	if ingest && cfg.EnsureSchema {
		err = ragindexer.Migrate(ctx, pool, cfg.Postgres)
	}
	if err == nil {
		err = ragindexer.CheckSchema(ctx, pool, cfg.Embedding.Dimensions)
	}
	if err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// embeddingProfile 由真实协议、地址、模型、版本及输入规则生成，导入和查询使用同一档案校验。
func embeddingProfile(cfg ragconfig.Config) ragembedding.Profile {
	return ragembedding.NewProfile(cfg.Embedding, cfg.InputBuilder)
}

func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
