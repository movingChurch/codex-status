//go:build darwin

// SPDX-License-Identifier: APSL-2.0
// Copyright (c) 2005-2021 Apple Inc. All rights reserved.
//
// @APPLE_OSREFERENCE_LICENSE_HEADER_START@
//
// This file contains Original Code and/or Modifications of Original Code
// as defined in and that are subject to the Apple Public Source License
// Version 2.0 (the 'License'). You may not use this file except in
// compliance with the License. The rights granted to you under the License
// may not be used to create, or enable the creation or redistribution of,
// unlawful or unlicensed copies of an Apple operating system, or to
// circumvent, violate, or enable the circumvention or violation of, any
// terms of an Apple operating system software license agreement.
//
// Please obtain a copy of the License at
// http://www.opensource.apple.com/apsl/ and read it before using this file.
//
// The Original Code and all software distributed under the License are
// distributed on an 'AS IS' basis, WITHOUT WARRANTY OF ANY KIND, EITHER
// EXPRESS OR IMPLIED, AND APPLE HEREBY DISCLAIMS ALL SUCH WARRANTIES,
// INCLUDING WITHOUT LIMITATION, ANY WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE, QUIET ENJOYMENT OR NON-INFRINGEMENT.
// Please see the License for the specific language governing rights and
// limitations under the License.
//
// @APPLE_OSREFERENCE_LICENSE_HEADER_END@
//
//
// Modified 2026-09-08 for runtime-status: selected C ABI declarations translated
// to Go byte buffers and native libproc calls.
// Original source: Apple macOS SDK, sys/proc_info.h.
// Public source reference:
// https://github.com/apple-oss-distributions/xnu/blob/f6217f891ac0bb64f3d375211650a4c1ff8ca1ea/bsd/sys/proc_info.h
// The complete license is distributed in LICENSES/APPLE-APSL-2.0.txt.

package telemetry

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The field offsets below preserve the SDK ABI measured by src/darwin_abi.py.
// Both supported Darwin architectures use this same 64-bit layout.
const bindingBSDSize = 136
const bindingVnodeSize = 1200

var bindingProc struct {
	once      sync.Once
	available bool
	pidinfo   func(int32, int32, uint64, unsafe.Pointer, int32) int32
	fdinfo    func(int32, int32, int32, unsafe.Pointer, int32) int32
	pidpath   func(int32, unsafe.Pointer, uint32) int32
}

func bindingLoadProc() bool {
	bindingProc.once.Do(func() {
		handle, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			return
		}
		names := []string{"proc_pidinfo", "proc_pidfdinfo", "proc_pidpath"}
		symbols := make([]uintptr, len(names))
		for i, name := range names {
			symbol, err := purego.Dlsym(handle, name)
			if err != nil {
				return
			}
			symbols[i] = symbol
		}
		purego.RegisterFunc(&bindingProc.pidinfo, symbols[0])
		purego.RegisterFunc(&bindingProc.fdinfo, symbols[1])
		purego.RegisterFunc(&bindingProc.pidpath, symbols[2])
		bindingProc.available = true
	})
	return bindingProc.available
}
func bindingDarwinIdentity(pid int, info []byte) string {
	if len(info) != bindingBSDSize {
		return ""
	}
	flags := binary.LittleEndian.Uint32(info[:4])
	actual := binary.LittleEndian.Uint32(info[12:16])
	sec := binary.LittleEndian.Uint64(info[120:128])
	usec := binary.LittleEndian.Uint64(info[128:136])
	if actual != uint32(pid) || flags&4 != 0 || sec == 0 || usec >= 1000000 {
		return ""
	}
	return fmt.Sprintf("darwin:%d:%06d", sec, usec)
}

// ProcessIdentity reads the kernel start time, retaining microsecond precision.
func ProcessIdentity(pid int) string {
	if pid <= 0 || pid > 2147483647 || !bindingLoadProc() {
		return ""
	}
	var info [bindingBSDSize]byte
	if bindingProc.pidinfo(int32(pid), 3, 0, unsafe.Pointer(&info[0]), bindingBSDSize) != bindingBSDSize {
		return ""
	}
	return bindingDarwinIdentity(pid, info[:])
}
func bindingProcessExecutable(pid int) string {
	if !bindingLoadProc() {
		return ""
	}
	var output [4096]byte
	length := bindingProc.pidpath(int32(pid), unsafe.Pointer(&output[0]), uint32(len(output)))
	if length <= 0 || length >= int32(len(output)) {
		return ""
	}
	end := bytes.IndexByte(output[:], 0)
	if end < 0 {
		return ""
	}
	return bindingResolve(string(output[:end]))
}
func bindingOpenRollouts(pid int) ([]openedRollout, bool) {
	if !bindingLoadProc() {
		return nil, false
	}
	needed := bindingProc.pidinfo(int32(pid), 1, 0, nil, 0)
	if needed <= 0 {
		return nil, false
	}
	capacity := int(needed)/8 + 32
	for attempt := 0; attempt < 3; attempt++ {
		if capacity > 16384 {
			return nil, false
		}
		output := make([]byte, capacity*8)
		length := bindingProc.pidinfo(int32(pid), 1, 0, unsafe.Pointer(&output[0]), int32(len(output)))
		if length <= 0 || length%8 != 0 || int(length) > len(output) {
			return nil, false
		}
		if int(length) == len(output) {
			capacity *= 2
			continue
		}
		paths := []openedRollout{}
		for offset := 0; offset < int(length); offset += 8 {
			fd := int32(binary.LittleEndian.Uint32(output[offset : offset+4]))
			kind := binary.LittleEndian.Uint32(output[offset+4 : offset+8])
			if kind != 1 {
				continue
			}
			var info [bindingVnodeSize]byte
			if bindingProc.fdinfo(int32(pid), fd, 2, unsafe.Pointer(&info[0]), bindingVnodeSize) != bindingVnodeSize {
				return nil, false
			}
			end := bytes.IndexByte(info[176:], 0)
			if end <= 0 {
				return nil, false
			}
			path := string(info[176 : 176+end])
			if strings.HasSuffix(path, ".jsonl") {
				paths = append(paths, openedRollout{path, rolloutIdentity{uint64(binary.LittleEndian.Uint32(info[24:28])), binary.LittleEndian.Uint64(info[32:40])}})
			}
		}
		return paths, true
	}
	return nil, false
}

func bindingChangeTime(info os.FileInfo) int64 { return info.Sys().(*syscall.Stat_t).Ctimespec.Nano() }
