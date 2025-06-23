#
# Copyright 2019-2021 Canonical Ltd.
# Authors:
# - dann frazier <dann.frazier@canonical.com>
#
# This program is free software: you can redistribute it and/or modify it
# under the terms of the GNU General Public License version 3, as published
# by the Free Software Foundation.
#
# This program is distributed in the hope that it will be useful, but WITHOUT
# ANY WARRANTY; without even the implied warranties of MERCHANTABILITY,
# SATISFACTORY QUALITY, or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
# General Public License for more details.
#
# You should have received a copy of the GNU General Public License along with
# this program.  If not, see <http://www.gnu.org/licenses/>.
#

import enum
import os
import shutil
import tempfile


class QemuEfiMachine(enum.Enum):
    OVMF_PC = enum.auto()
    OVMF_Q35 = enum.auto()
    OVMF32 = enum.auto()
    AAVMF = enum.auto()
    AAVMF32 = enum.auto()


class QemuEfiVariant(enum.Enum):
    MS = enum.auto()
    SECBOOT = enum.auto()
    SNAKEOIL = enum.auto()


class QemuEfiFlashSize(enum.Enum):
    DEFAULT = enum.auto()
    SIZE_2MB = enum.auto()
    SIZE_4MB = enum.auto()


class QemuCommand:
    Qemu_Common_Params = [
        '-no-user-config', '-nodefaults',
        '-m', '256',
        '-smp', '1,sockets=1,cores=1,threads=1',
        '-display', 'none',
        '-serial', 'stdio',
    ]
    Ovmf_Common_Params = Qemu_Common_Params + [
        '-chardev', 'pty,id=charserial1',
        '-device', 'isa-serial,chardev=charserial1,id=serial1',
    ]
    Aavmf_Common_Params = Qemu_Common_Params + [
        '-machine', 'virt', '-device', 'virtio-serial-device',
    ]
    Machine_Base_Command = {
        QemuEfiMachine.AAVMF: [
            'qemu-system-aarch64', '-cpu', 'cortex-a57',
        ] + Aavmf_Common_Params,
        QemuEfiMachine.AAVMF32: [
            'qemu-system-aarch64', '-cpu', 'cortex-a15',
        ] + Aavmf_Common_Params,
        QemuEfiMachine.OVMF_PC: [
            'qemu-system-x86_64', '-machine', 'pc,accel=tcg',
        ] + Ovmf_Common_Params,
        QemuEfiMachine.OVMF_Q35: [
            'qemu-system-x86_64', '-machine', 'q35,accel=tcg',
        ] + Ovmf_Common_Params,
        QemuEfiMachine.OVMF32: [
            'qemu-system-i386', '-machine', 'q35,accel=tcg',
        ] + Ovmf_Common_Params,
    }

    def _get_default_flash_paths(self, machine, variant, flash_size):
        assert(machine in QemuEfiMachine)
        assert(variant is None or variant in QemuEfiVariant)
        assert(flash_size in QemuEfiFlashSize)

        code_ext = vars_ext = ''
        if variant == QemuEfiVariant.MS:
            code_ext = vars_ext = '.ms'
        elif variant == QemuEfiVariant.SECBOOT:
            code_ext = '.secboot'
        elif variant == QemuEfiVariant.SNAKEOIL:
            vars_ext = '.snakeoil'

        if machine == QemuEfiMachine.AAVMF:
            assert(flash_size == QemuEfiFlashSize.DEFAULT)
            return (
                f'/usr/share/AAVMF/AAVMF_CODE{code_ext}.fd',
                f'/usr/share/AAVMF/AAVMF_VARS{code_ext}.fd',
            )
        if machine == QemuEfiMachine.AAVMF32:
            assert(variant is None)
            assert(flash_size == QemuEfiFlashSize.DEFAULT)
            return (
                '/usr/share/AAVMF/AAVMF32_CODE.fd',
                '/usr/share/AAVMF/AAVMF32_VARS.fd'
            )
        if machine == QemuEfiMachine.OVMF32:
            assert(variant is None or variant in [QemuEfiVariant.SECBOOT])
            assert(
                flash_size in [
                    QemuEfiFlashSize.DEFAULT, QemuEfiFlashSize.SIZE_4MB
                ]
            )
            return (
                '/usr/share/OVMF/OVMF32_CODE_4M.secboot.fd',
                '/usr/share/OVMF/OVMF32_VARS_4M.fd',
            )
        # Remaining possibilities are OVMF variants
        if machine == QemuEfiMachine.OVMF_PC:
            assert(variant is None)
        if variant == QemuEfiVariant.SNAKEOIL:
            # We provide one size - you don't get to pick.
            assert(flash_size == QemuEfiFlashSize.DEFAULT)
        size_ext = '' if flash_size == QemuEfiFlashSize.SIZE_2MB else '_4M'
        return (
            f'/usr/share/OVMF/OVMF_CODE{size_ext}{code_ext}.fd',
            f'/usr/share/OVMF/OVMF_VARS{size_ext}{vars_ext}.fd'
        )

    def __init__(
            self, machine, variant=None,
            code_path=None, vars_template_path=None,
            flash_size=QemuEfiFlashSize.DEFAULT,
    ):
        assert(
            (code_path and vars_template_path) or
            (not code_path and not vars_template_path)
        )

        if not code_path:
            (code_path, vars_template_path) = self._get_default_flash_paths(
                machine, variant, flash_size)

        self.pflash = self.PflashParams(code_path, vars_template_path)
        self.command = self.Machine_Base_Command[machine] + self.pflash.params
        if variant in [QemuEfiVariant.MS, QemuEfiVariant.SECBOOT] and \
           flash_size == QemuEfiFlashSize.SIZE_2MB:
            # 2MB images have 64-bit PEI that does not support S3 w/ SMM
            self.command.extend(['-global', 'ICH9-LPC.disable_s3=1'])

    def add_disk(self, path):
        self.command = self.command + [
            '-drive', 'file=%s,format=raw' % (path)
        ]

    def add_oem_string(self, type, string):
        string = string.replace(",", ",,")
        self.command = self.command + [
            '-smbios', f'type={type},value={string}'
        ]

    class PflashParams:
        '''
        Used to generate the appropriate -pflash arguments for QEMU. Mostly
        used as a fancy way to generate a per-instance vars file and have it
        be automatically cleaned up when the object is destroyed.
        '''
        def __init__(self, code_path, vars_template_path):
            with tempfile.NamedTemporaryFile(delete=False) as varfile:
                self.varfile_path = varfile.name
                with open(vars_template_path, 'rb') as template:
                    shutil.copyfileobj(template, varfile)
                self.params = [
                    '-drive',
                    'file=%s,if=pflash,format=raw,unit=0,readonly=on' %
                    (code_path),
                    '-drive',
                    'file=%s,if=pflash,format=raw,unit=1,readonly=off' %
                    (varfile.name)
                ]

        def __del__(self):
            os.unlink(self.varfile_path)
