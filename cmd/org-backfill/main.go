// Command org-backfill explicitly initializes old organization event snapshots.
// It never starts ARTEX, applies schema, or connects without ARTEX_PG_DSN.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Autumn-27/artex/internal/org"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(parent context.Context, args []string, output, errors io.Writer) int {
	flags := flag.NewFlagSet("org-backfill", flag.ContinueOnError)
	flags.SetOutput(errors)
	apply := flags.Bool("apply", false, "写入回填结果；省略时仅查看，不修改数据")
	batchSize := flags.Int("batch-size", 200, "每批最多处理 1 至 500 条")
	limit := flags.Duration("timeout", 5*time.Minute, "本次运行总时限；中断后可重复执行继续")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *limit <= 0 || *limit > time.Hour {
		fmt.Fprintln(errors, "请使用命名参数，timeout 须大于 0 且不超过 1 小时")
		return 2
	}
	dsn := os.Getenv("ARTEX_PG_DSN")
	if dsn == "" {
		fmt.Fprintln(errors, "请明确设置 ARTEX_PG_DSN；本工具不自动读取服务配置或创建数据库")
		return 2
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		fmt.Fprintln(errors, "ARTEX_PG_DSN 格式不正确")
		return 2
	}
	config.ConnectTimeout = 10 * time.Second
	config.RuntimeParams["statement_timeout"] = "30000"
	config.RuntimeParams["lock_timeout"] = "5000"
	if !*apply {
		config.RuntimeParams["default_transaction_read_only"] = "on"
	}
	database := stdlib.OpenDB(*config)
	database.SetMaxOpenConns(1)
	defer database.Close()
	ctx, cancel := context.WithTimeout(parent, *limit)
	defer cancel()
	if err = database.PingContext(ctx); err != nil {
		fmt.Fprintln(errors, "连接失败，请检查数据库地址、权限及服务状态")
		return 1
	}
	store := org.New(database)
	encode := json.NewEncoder(output)
	preview, err := store.PreviewHistoryBackfill(ctx)
	if err == nil {
		err = encode.Encode(map[string]any{"phase": "preview", "apply": *apply, "result": preview})
	}
	if err == nil && *apply {
		for {
			batchContext, finish := context.WithTimeout(ctx, 30*time.Second)
			var result org.HistoryBackfillAssets
			result, err = store.PrepareHistoryBackfillAssets(batchContext, *batchSize)
			finish()
			if err != nil {
				break
			}
			if err = encode.Encode(map[string]any{"phase": "prepare_current_assets", "result": result}); err != nil || result.Done {
				break
			}
		}
		for err == nil {
			batchContext, finish := context.WithTimeout(ctx, 30*time.Second)
			result, batchErr := store.BackfillHistoryBatch(batchContext, *batchSize)
			finish()
			if batchErr != nil {
				err = batchErr
				break
			}
			if err = encode.Encode(map[string]any{"phase": "history", "result": result}); err != nil || result.Done {
				if err == nil && result.Done && result.CurrentRemaining > 0 {
					fmt.Fprintf(errors, "本次固定范围已完成，另有 %d 条范围外的旧记录尚未整理，请先核对其来源。\n", result.CurrentRemaining)
				}
				break
			}
		}
	}
	if err != nil {
		fmt.Fprintf(errors, "回填未完成：%v\n已提交批次保留；排除原因后使用同一命令继续。\n", err)
		return 1
	}
	return 0
}
