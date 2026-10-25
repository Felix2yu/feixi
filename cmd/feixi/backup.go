package main

import (
	"io"

	"github.com/Felix2yu/feixi/internal/config"
	"github.com/Felix2yu/feixi/internal/store"
)

// runBackup 实现 `feixi backup`：把一份 SQLite 一致性快照写到 w（由 main 传 os.Stdout），
// 供外部备份工具（docker-db-auto-backup 等）经 docker exec 收取。
//
// 日志一律走 stderr，stdout 只承载快照字节。库路径沿用 FEIXI_DB 的解析，工具侧
// 不需要额外配置；导出经 OpenForExport 打开，不跑迁移、不建目录。
func runBackup(w io.Writer) error {
	db, err := store.OpenForExport(config.Default().DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.WriteSQLiteSnapshot(w)
}
