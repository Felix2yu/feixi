package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteSQLiteSnapshot 把数据库以 SQLite 自身的一致性快照写到 w。
//
// 用 VACUUM INTO 而不是复制文件：热拷贝正在被写入的 db/-wal/-shm 不是原子操作，
// 周期性地拷会间歇性产出坏副本，还可能漏掉 WAL 里已提交的数据；VACUUM INTO 由
// SQLite 在事务里生成一份自洽的快照，WAL 模式下也不阻塞写入。
//
// 快照先落在临时目录再流式输出：VACUUM INTO 要求目标不存在，且这样无论外部备份
// 工具读到一半断开（docker exec 被掐掉），都不会在数据目录里留下半成品。
func (db *DB) WriteSQLiteSnapshot(w io.Writer) error {
	dir, err := os.MkdirTemp("", "feixi-snapshot-")
	if err != nil {
		return fmt.Errorf("创建临时目录：%w", err)
	}
	defer os.RemoveAll(dir)

	target := filepath.Join(dir, "snapshot.db")
	if _, err := db.Write.Exec("VACUUM INTO ?", target); err != nil {
		return fmt.Errorf("导出数据库快照：%w", err)
	}

	f, err := os.Open(target)
	if err != nil {
		return fmt.Errorf("读取快照：%w", err)
	}
	defer f.Close()
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("写出快照：%w", err)
	}
	return nil
}
