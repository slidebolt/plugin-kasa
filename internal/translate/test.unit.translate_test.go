package translate

import (
	"encoding/json"
	"testing"
)

func TestSysInfo_UnmarshalChildren(t *testing.T) {
	raw := []byte(`{
		"system": {
			"get_sysinfo": {
				"model": "EP40(US)",
				"deviceId": "80067FCB6D318DBCDED89309B7249B791FEFC423",
				"alias": "TP-LINK_Smart Plug_0A49",
				"mac": "28:87:BA:95:0A:49",
				"children": [
					{"id": "80067FCB6D318DBCDED89309B7249B791FEFC42300", "state": 1, "alias": "deck lights "},
					{"id": "80067FCB6D318DBCDED89309B7249B791FEFC42301", "state": 0, "alias": "deck holiday lights"}
				],
				"child_num": 2
			}
		}
	}`)

	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.System.SysInfo.DeviceID != "80067FCB6D318DBCDED89309B7249B791FEFC423" {
		t.Fatalf("deviceId = %q", resp.System.SysInfo.DeviceID)
	}
	if resp.System.SysInfo.ChildNum != 2 {
		t.Fatalf("child_num = %d, want 2", resp.System.SysInfo.ChildNum)
	}
	if len(resp.System.SysInfo.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(resp.System.SysInfo.Children))
	}
	if resp.System.SysInfo.Children[0].Alias != "deck lights " {
		t.Fatalf("child alias = %q", resp.System.SysInfo.Children[0].Alias)
	}
	if resp.System.SysInfo.Children[1].RelayState != 0 {
		t.Fatalf("child relay state = %d, want 0", resp.System.SysInfo.Children[1].RelayState)
	}
}

func TestMakeChildEntityID(t *testing.T) {
	got := MakeChildEntityID(
		"80067FCB6D318DBCDED89309B7249B791FEFC423",
		"80067FCB6D318DBCDED89309B7249B791FEFC42301",
	)
	if got != "outlet-01" {
		t.Fatalf("MakeChildEntityID() = %q, want %q", got, "outlet-01")
	}
}

func TestNormalizeChildID(t *testing.T) {
	tests := []struct {
		name     string
		parentID string
		childID  string
		want     string
	}{
		{
			name:     "expands short suffix",
			parentID: "80067FCB6D318DBCDED89309B7249B791FEFC423",
			childID:  "01",
			want:     "80067FCB6D318DBCDED89309B7249B791FEFC42301",
		},
		{
			name:     "passes through full child id",
			parentID: "80067FCB6D318DBCDED89309B7249B791FEFC423",
			childID:  "80067FCB6D318DBCDED89309B7249B791FEFC42300",
			want:     "80067FCB6D318DBCDED89309B7249B791FEFC42300",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeChildID(tt.parentID, tt.childID); got != tt.want {
				t.Fatalf("NormalizeChildID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseCommandError_Success(t *testing.T) {
	got, err := parseCommandError(`{"system":{"set_relay_state":{"err_code":0}}}`)
	if err != nil {
		t.Fatalf("parseCommandError() error = %v", err)
	}
	if got.Path != "" {
		t.Fatalf("parseCommandError() = %+v, want no error", got)
	}
}

func TestParseCommandError_NonZeroErrCode(t *testing.T) {
	got, err := parseCommandError(`{"system":{"set_relay_state":{"err_code":-1}}}`)
	if err != nil {
		t.Fatalf("parseCommandError() error = %v", err)
	}
	if got.Path != "system.set_relay_state.err_code" || got.ErrCode != -1 {
		t.Fatalf("parseCommandError() = %+v", got)
	}
}

func TestParseCommandError_InvalidJSON(t *testing.T) {
	if _, err := parseCommandError(`not-json`); err == nil {
		t.Fatal("parseCommandError() error = nil, want decode error")
	}
}
