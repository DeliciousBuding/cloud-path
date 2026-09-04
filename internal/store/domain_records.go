package store

import "database/sql"

// AppDomainRecordRow 是 app_domain_records 一行（Application Plugin 的领域记录）。
type AppDomainRecordRow struct {
	TenantID   int64
	InstanceID string
	RecordType string
	RecordID   string
	DataJSON   string
	Version    string
	UpdatedAt  int64
}

// UpsertAppDomainRecord 写入/覆盖一条应用领域记录（create_domain_record 效果落点）。
// tenantID 由调用方（Server，来自实例行）传入；记录键为
// (tenant, instance, record_type, record_id)，同一键重复上报即覆盖——
// 与 Application Protocol 的 UpsertDomainRecord 语义一致。
func (s *Store) UpsertAppDomainRecord(tenantID int64, instanceID, recordType, recordID, dataJSON, version string, updatedAt int64) error {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return err
	}
	_, err = s.exec(`
		INSERT INTO app_domain_records(tenant_id, instance_id, record_type, record_id, data_json, version, updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id, instance_id, record_type, record_id) DO UPDATE SET
			data_json=excluded.data_json, version=excluded.version, updated_at=excluded.updated_at`,
		tid, instanceID, recordType, recordID, dataJSON, version, updatedAt)
	return err
}

// ListAppDomainRecords 按实例列领域记录（按更新时间倒序，limit<=0 默认 100，上限 1000）。
func (s *Store) ListAppDomainRecords(tenantID int64, instanceID string, limit int) ([]AppDomainRecordRow, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`
		SELECT tenant_id, instance_id, record_type, record_id, data_json, version, updated_at
		FROM app_domain_records WHERE tenant_id=? AND instance_id=?
		ORDER BY updated_at DESC, record_id LIMIT ?`, tid, instanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppDomainRecordRow
	for rows.Next() {
		var r AppDomainRecordRow
		if err := rows.Scan(&r.TenantID, &r.InstanceID, &r.RecordType, &r.RecordID, &r.DataJSON, &r.Version, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetAppDomainRecord 取单条领域记录；不存在返回 sql.ErrNoRows。
func (s *Store) GetAppDomainRecord(tenantID int64, instanceID, recordType, recordID string) (AppDomainRecordRow, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return AppDomainRecordRow{}, err
	}
	var r AppDomainRecordRow
	err = s.db.QueryRow(`
		SELECT tenant_id, instance_id, record_type, record_id, data_json, version, updated_at
		FROM app_domain_records WHERE tenant_id=? AND instance_id=? AND record_type=? AND record_id=?`,
		tid, instanceID, recordType, recordID).
		Scan(&r.TenantID, &r.InstanceID, &r.RecordType, &r.RecordID, &r.DataJSON, &r.Version, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return AppDomainRecordRow{}, sql.ErrNoRows
	}
	return r, err
}
