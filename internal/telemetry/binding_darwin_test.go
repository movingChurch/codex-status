//go:build darwin

package telemetry

import (
	"encoding/binary"
	"testing"
)

func TestBindingDarwinIdentityPrecision(t *testing.T) {
	info := make([]byte, 136)
	binary.LittleEndian.PutUint32(info[12:16], 123)
	binary.LittleEndian.PutUint64(info[120:128], 1800000000)
	binary.LittleEndian.PutUint64(info[128:136], 1)
	first := bindingDarwinIdentity(123, info)
	if first != "darwin:1800000000:000001" {
		t.Fatal(first)
	}
	binary.LittleEndian.PutUint64(info[128:136], 2)
	if first == bindingDarwinIdentity(123, info) {
		t.Fatal("microseconds lost")
	}
	if bindingDarwinIdentity(124, info) != "" {
		t.Fatal("wrong PID accepted")
	}
	if bindingDarwinIdentity(123, info[:135]) != "" {
		t.Fatal("partial ABI accepted")
	}
	binary.LittleEndian.PutUint64(info[128:136], 1000000)
	if bindingDarwinIdentity(123, info) != "" {
		t.Fatal("invalid microseconds accepted")
	}
	binary.LittleEndian.PutUint64(info[128:136], 1)
	binary.LittleEndian.PutUint32(info[0:4], 4)
	if bindingDarwinIdentity(123, info) != "" {
		t.Fatal("exiting process accepted")
	}
}
