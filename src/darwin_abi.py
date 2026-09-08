# SPDX-License-Identifier: APSL-2.0
# Copyright (c) 2005-2021 Apple Inc. All rights reserved.
#
# @APPLE_OSREFERENCE_LICENSE_HEADER_START@
#
# This file contains Original Code and/or Modifications of Original Code
# as defined in and that are subject to the Apple Public Source License
# Version 2.0 (the 'License'). You may not use this file except in
# compliance with the License. The rights granted to you under the License
# may not be used to create, or enable the creation or redistribution of,
# unlawful or unlicensed copies of an Apple operating system, or to
# circumvent, violate, or enable the circumvention or violation of, any
# terms of an Apple operating system software license agreement.
#
# Please obtain a copy of the License at
# http://www.opensource.apple.com/apsl/ and read it before using this file.
#
# The Original Code and all software distributed under the License are
# distributed on an 'AS IS' basis, WITHOUT WARRANTY OF ANY KIND, EITHER
# EXPRESS OR IMPLIED, AND APPLE HEREBY DISCLAIMS ALL SUCH WARRANTIES,
# INCLUDING WITHOUT LIMITATION, ANY WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE, QUIET ENJOYMENT OR NON-INFRINGEMENT.
# Please see the License for the specific language governing rights and
# limitations under the License.
#
# @APPLE_OSREFERENCE_LICENSE_HEADER_END@
#
#
# Modified 2026-09-08 for codex-status: selected C ABI declarations translated
# to Python ctypes, field names shortened, and measured layout checks added.
# Original source: Apple macOS SDK, sys/proc_info.h.
# Public source reference:
# https://github.com/apple-oss-distributions/xnu/blob/f6217f891ac0bb64f3d375211650a4c1ff8ca1ea/bsd/sys/proc_info.h
# The complete license is distributed in LICENSES/APPLE-APSL-2.0.txt.

"""Apple-derived Darwin ABI declarations, distributed under APSL-2.0."""

import ctypes

# Darwin ABI mirrored from the installed SDK's sys/proc_info.h.
class _BsdInfo(ctypes.Structure):
    _fields_ = [(name, ctypes.c_uint32) for name in (
        "flags", "status", "xstatus", "pid", "ppid", "uid", "gid", "ruid",
        "rgid", "svuid", "svgid", "reserved")]
    _fields_ += [("comm", ctypes.c_char * 16), ("name", ctypes.c_char * 32)]
    _fields_ += [(name, ctypes.c_uint32) for name in (
        "nfiles", "pgid", "pjobc", "tdev", "tpgid")]
    _fields_ += [("nice", ctypes.c_int32), ("start_sec", ctypes.c_uint64),
                 ("start_usec", ctypes.c_uint64)]


class _FdInfo(ctypes.Structure):
    _fields_ = [("fd", ctypes.c_int32), ("kind", ctypes.c_uint32)]


class _FileInfo(ctypes.Structure):
    _fields_ = [("openflags", ctypes.c_uint32), ("status", ctypes.c_uint32),
                ("offset", ctypes.c_int64), ("kind", ctypes.c_int32),
                ("guardflags", ctypes.c_uint32)]


class _VInfoStat(ctypes.Structure):
    _fields_ = [("dev", ctypes.c_uint32), ("mode", ctypes.c_uint16),
                ("nlink", ctypes.c_uint16), ("ino", ctypes.c_uint64),
                ("uid", ctypes.c_uint32), ("gid", ctypes.c_uint32)]
    _fields_ += [(name, ctypes.c_int64) for name in (
        "atime", "atimensec", "mtime", "mtimensec", "ctime", "ctimensec",
        "birthtime", "birthtimensec", "size", "blocks")]
    _fields_ += [("blksize", ctypes.c_int32), ("flags", ctypes.c_uint32),
                 ("gen", ctypes.c_uint32), ("rdev", ctypes.c_uint32),
                 ("qspare", ctypes.c_int64 * 2)]


class _VnodeInfo(ctypes.Structure):
    _fields_ = [("stat", _VInfoStat), ("kind", ctypes.c_int),
                ("pad", ctypes.c_int), ("fsid", ctypes.c_int32 * 2)]


class _VnodePath(ctypes.Structure):
    _fields_ = [("info", _VnodeInfo), ("path", ctypes.c_char * 1024)]


class _VnodeFdPath(ctypes.Structure):
    _fields_ = [("file", _FileInfo), ("vnode", _VnodePath)]


def _ctypes_layout():
    return {
        "proc_bsdinfo": ctypes.sizeof(_BsdInfo),
        "start_sec": _BsdInfo.start_sec.offset,
        "start_usec": _BsdInfo.start_usec.offset,
        "proc_fdinfo": ctypes.sizeof(_FdInfo),
        "proc_fileinfo": ctypes.sizeof(_FileInfo),
        "vinfo_stat": ctypes.sizeof(_VInfoStat),
        "vnode_info": ctypes.sizeof(_VnodeInfo),
        "vnode_info_path": ctypes.sizeof(_VnodePath),
        "vnode_fdinfowithpath": ctypes.sizeof(_VnodeFdPath),
        "vnode_path_offset": _VnodeFdPath.vnode.offset + _VnodePath.path.offset,
        "vnode_dev_offset": _VnodeFdPath.vnode.offset + _VnodePath.info.offset + _VnodeInfo.stat.offset + _VInfoStat.dev.offset,
        "vnode_ino_offset": _VnodeFdPath.vnode.offset + _VnodePath.info.offset + _VnodeInfo.stat.offset + _VInfoStat.ino.offset,
        "max_path": 1024,
        "max_comm": 16,
    }


_SDK_LAYOUT = {
    "proc_bsdinfo": 136, "start_sec": 120, "start_usec": 128,
    "proc_fdinfo": 8, "proc_fileinfo": 24, "vinfo_stat": 136,
    "vnode_info": 152, "vnode_info_path": 1176,
    "vnode_fdinfowithpath": 1200, "vnode_path_offset": 176,
    "vnode_dev_offset": 24, "vnode_ino_offset": 32,
    "max_path": 1024, "max_comm": 16,
}
