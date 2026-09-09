package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func TestMarkCommandsHandledEndpoint(t *testing.T) {
	st, _, ts, _, tenantID, _ := setupOverview(t)
	if err := st.UpsertDeviceTenant("e1/d1", "e1", "demo", "设备", "COM3", tenantID); err != nil {
		t.Fatal(err)
	}
	commandID, err := st.CreateCommandTenant("e1/d1", "ping", "", tenantID)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := st.UpdateCommandStatusScoped(commandID, "e1/d1", tenantID, "failed", "busy")
	if err != nil || !ok {
		t.Fatalf("set failed status: ok=%v err=%v", ok, err)
	}
	writeToken := issueTenantToken(t, st, tenantID, `["write"]`)
	readToken := issueTenantToken(t, st, tenantID, `["read"]`)

	resp := doJSON(t, http.MethodPost, ts.URL+"/api/commands/handled", `{"ids":[`+strconv.FormatInt(commandID, 10)+`]}`, bearerJSON(writeToken), nil)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST handled = %d body=%s", resp.StatusCode, body)
	}
	var reply map[string]int64
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatal(err)
	}
	if reply["handled"] != 1 {
		t.Fatalf("handled reply = %+v", reply)
	}
	view, raw := getOverview(t, ts, readToken)
	if view.CommandsFailed != 0 || len(view.FailedCommands) != 0 {
		t.Fatalf("handled command still in overview: %+v (%s)", view, raw)
	}
	rows, err := st.ListCommandsTenantFiltered(tenantID, "", "", "handled", 10)
	if err != nil || len(rows) != 1 || !rows[0].HandledAt.Valid {
		t.Fatalf("handled row missing: %+v err=%v", rows, err)
	}
}
