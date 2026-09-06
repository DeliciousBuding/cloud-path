package driver

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Golden JSON comes from the language-neutral Driver Describe contract, not
// ActionDescriptor construction: both missing decode fields and missing tags fail.
func TestActionDescriptorJSONMetadataCompatibility(t *testing.T) {
	legacy := `{"name":"legacy_action","input_schema_json":"{\"type\":\"object\"}","result_schema_json":"{}"}`
	cases := []struct {
		name string
		wire string
		want string
	}{
		{"metadata", `{"name":"calibrate","input_schema_json":"{\"type\":\"object\",\"required\":[\"offset\"]}","result_schema_json":"{\"type\":\"boolean\"}","title":"标定","description":"  更新偏移量  ","destructive":true,"confirmation":"执行「标定」？\n设备设置将更新。"}`, ""},
		{"legacy", legacy, legacy},
		{"zero_metadata", `{"name":"legacy_action","input_schema_json":"{\"type\":\"object\"}","result_schema_json":"{}","title":"","description":"","destructive":false,"confirmation":""}`, legacy},
		{"null_metadata", `{"name":"legacy_action","input_schema_json":"{\"type\":\"object\"}","result_schema_json":"{}","title":null,"description":null,"destructive":null,"confirmation":null}`, legacy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var action ActionDescriptor
			if err := json.Unmarshal([]byte(tc.wire), &action); err != nil {
				t.Fatalf("decode action: %v", err)
			}
			encoded, err := json.Marshal(action)
			if err != nil {
				t.Fatalf("encode action: %v", err)
			}
			wantJSON := tc.want
			if wantJSON == "" {
				wantJSON = tc.wire
			}
			var got, want map[string]any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("wire metadata changed:\n got %s\nwant %s", encoded, wantJSON)
			}

			// An old peer still consumes the three original keys; new metadata is
			// optional and must not alter name, input schema or result schema.
			var oldPeer struct {
				Name             string `json:"name"`
				InputSchemaJSON  string `json:"input_schema_json"`
				ResultSchemaJSON string `json:"result_schema_json"`
			}
			if err := json.Unmarshal(encoded, &oldPeer); err != nil {
				t.Fatalf("old peer decode: %v", err)
			}
			if oldPeer.Name != action.Name || oldPeer.InputSchemaJSON != action.InputSchemaJSON || oldPeer.ResultSchemaJSON != action.ResultSchemaJSON {
				t.Fatalf("legacy fields changed: %+v", oldPeer)
			}
		})
	}
}
