package store

import (
	"errors"
	"strings"
	"testing"
)

func TestSetDeviceDescriptor(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertDevice("e1/d1", "e1", "demo", "节点1", "COM3"); err != nil {
		t.Fatal(err)
	}
	want := `{"device_id":"e1/d1","external_id":"d1","status":"offline","entities":[]}`
	if err := s.SetDeviceDescriptor("e1/d1", want); err != nil {
		t.Fatalf("SetDeviceDescriptor: %v", err)
	}
	devs, err := s.ListDevices()
	if err != nil || len(devs) != 1 {
		t.Fatalf("ListDevices = %+v err=%v", devs, err)
	}
	if devs[0].DescriptorJSON != want {
		t.Fatalf("descriptor_json = %q, want %q", devs[0].DescriptorJSON, want)
	}

	if err := s.SetDeviceDescriptor("e1/missing", want); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("unknown device err = %v, want ErrDeviceNotFound", err)
	}
	for _, tc := range []struct {
		name string
		raw  string
		want error
	}{
		{"empty", "", ErrDeviceDescriptorEmpty},
		{"whitespace", "   \n", ErrDeviceDescriptorEmpty},
		{"oversize", strings.Repeat("x", maxDeviceDescriptorJSONBytes+1), ErrDeviceDescriptorTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SetDeviceDescriptor("e1/d1", tc.raw); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	devs, err = s.ListDevices()
	if err != nil || len(devs) != 1 || devs[0].DescriptorJSON != want {
		t.Fatalf("failed writes changed descriptor: %+v err=%v", devs, err)
	}
}
