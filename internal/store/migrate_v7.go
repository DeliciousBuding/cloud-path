package store

import (
	"context"
	"database/sql"
	"fmt"
)

// applyIdempotentDDL 执行「全幂等 DDL」型迁移（v7/v8：只含 CREATE TABLE/INDEX IF NOT EXISTS）。
// 与 migrate.go 的 applyDDL 同一安全模型：BEGIN IMMEDIATE 写锁内重读 user_version（并发已推进则
// 空回滚释放锁），DDL 与 PRAGMA user_version 在同一事务内原子提交，崩溃不留半迁移。
// 额外两步收紧：提交前逐表校验目标表确实存在；提交后跑 foreign_key_check（沿用 migrateV5 验收）。
// 因为 DDL 幂等，「DDL 已应用但 user_version 未前进」的半迁移库重跑即自愈：不重复建表、不丢数据。
func applyIdempotentDDL(ctx context.Context, conn *sql.Conn, version int, ddl string, tables []string) error {
	if err := beginImmediate(ctx, conn); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			rollback(ctx, conn)
		}
	}()

	cur, err := readUserVersion(ctx, conn)
	if err != nil {
		return err
	}
	if cur >= version {
		return nil // 并发已推进：空回滚释放写锁
	}
	if _, err := conn.ExecContext(ctx, ddl); err != nil {
		return err
	}
	for _, table := range tables {
		ok, err := tableExists(ctx, conn, table)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("store: schema v%d: 表 %s 未创建", version, table)
		}
	}
	if err := setUserVersion(ctx, conn, version); err != nil {
		return err
	}
	if err := callMigrationHook(version, "before_commit"); err != nil {
		return err
	}
	if err := commit(ctx, conn); err != nil {
		return err
	}
	committed = true
	return foreignKeyCheck(ctx, conn)
}

// migrateV7 建插件控制面四表（docs/architecture/control-plane-sync.md §5）：
// plugin_desired_instances（Server 权威期望态）、plugin_edge_revisions（每 tenant/edge 同步游标）、
// plugin_installations 与 plugin_observations（Edge 上报只读投影）。
// 纯新增表，不改写任何既有表，因此 v6 数据零风险；主键首列恒为 tenant_id。
func migrateV7(ctx context.Context, conn *sql.Conn) error {
	return applyIdempotentDDL(ctx, conn, 7, schemaV7, []string{
		"plugin_desired_instances",
		"plugin_edge_revisions",
		"plugin_installations",
		"plugin_observations",
	})
}
