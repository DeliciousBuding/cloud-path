package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPluginControlMessageTypesStable(t *testing.T) {
	got := map[MsgType]string{
		MsgPluginStatus:  "plugin_status",
		MsgPluginDesired: "plugin_desired",
		MsgPluginAck:     "plugin_ack",
	}
	for typ, want := range got {
		if string(typ) != want {
			t.Fatalf("message type = %q, want %q", typ, want)
		}
	}
}

func TestPluginStatusContractRoundTrip(t *testing.T) {
	in := PluginStatusData{
		BootID:          "boot-1",
		Sequence:        7,
		AppliedRevision: 3,
		Installations: []PluginInstallationStatusData{{
			PluginID: "io.example.driver", Version: "1.2.3", Kind: "Driver", Protocol: 1,
			Digest: "abc", TrustMode: "verified-registry", Verified: true,
			Permissions:   PluginPermissionsData{Hardware: []string{"serial"}, Secrets: []string{"api_token"}},
			Contributions: PluginContributionsData{Drivers: []PluginDriverContributionData{{ID: "example"}}},
		}},
		ObservedInstances: []PluginObservedInstanceData{{
			InstanceID: "instance-1", PluginID: "io.example.driver", Version: "1.2.3",
			HostOnline: true, State: "RUNNING", Health: "HEALTHY", RestartCount: 2,
		}},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out PluginStatusData
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip mismatch:\nin=%+v\nout=%+v", in, out)
	}
	text := string(data)
	for _, forbidden := range []string{`"tenant"`, `"edge_id"`, `"path"`, `"config"`, `"env"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("plugin status leaked forbidden field %s: %s", forbidden, text)
		}
	}
}

func TestPluginDesiredContractRoundTrip(t *testing.T) {
	in := PluginDesiredData{
		Revision:       4,
		SnapshotDigest: "sha256:example",
		Instances: []PluginDesiredInstanceData{{
			InstanceID: "instance-1", PluginID: "io.example.app", Version: "0.1.0",
			Enabled: true, Isolation: "per-instance",
			Config: map[string]string{"endpoint": "https://example.invalid", "token": "secret://api_token"},
		}},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out PluginDesiredData
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip mismatch:\nin=%+v\nout=%+v", in, out)
	}
	if strings.Contains(string(data), `"tenant"`) || strings.Contains(string(data), `"edge_id"`) {
		t.Fatalf("desired payload must inherit authenticated edge identity: %s", data)
	}
}

func TestPluginAckContractRoundTrip(t *testing.T) {
	for _, status := range []string{PluginAckApplied, PluginAckRejected, PluginAckFailed} {
		in := PluginAckData{
			Revision: 9, SnapshotDigest: "sha256:example", Status: status,
			Results: []PluginApplyResultData{{InstanceID: "instance-1", Status: status, Detail: "bounded detail"}},
		}
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out PluginAckData
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("status %q round trip mismatch: in=%+v out=%+v", status, in, out)
		}
	}
}

func TestPluginStatusStructHasNoSensitiveTransportFields(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(PluginInstallationStatusData{}),
		reflect.TypeOf(PluginObservedInstanceData{}),
		reflect.TypeOf(PluginStatusData{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, forbidden := range []string{"path", "config", "secretvalue", "environment", "token"} {
				if strings.Contains(name, forbidden) {
					t.Fatalf("%s.%s is forbidden in edge status transport", typ.Name(), typ.Field(i).Name)
				}
			}
		}
	}
}
