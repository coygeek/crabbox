"""Contract checks without compiling UTM or touching installed applications."""

import argparse
import io
import json
import plistlib
import shutil
import signal
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import utm_workload as workload


class WorkloadTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)

    def tearDown(self):
        self.temporary.cleanup()

    def test_reusing_successful_output_cannot_satisfy_new_run(self):
        run = workload.Run(self.root / "proof", "build")
        run.result.update(status="passed", build="passed", package="passed")
        run.flush()
        original = (run.evidence / "result.json").read_bytes()
        with self.assertRaisesRegex(RuntimeError, "stale proof rejected"):
            workload.Run(run.root, "build")
        self.assertEqual(original, (run.evidence / "result.json").read_bytes())

    def test_archive_escape_is_rejected_without_external_write(self):
        archive = self.root / "sysroot.tgz"
        with tarfile.open(archive, "w:gz") as tar:
            item = tarfile.TarInfo("../outside")
            item.size = 7
            tar.addfile(item, io.BytesIO(b"escaped"))
        destination = self.root / "extract"
        destination.mkdir()
        with self.assertRaises(tarfile.FilterError):
            workload.extract_sysroot(archive, destination)
        self.assertFalse((self.root / "outside").exists())

    def test_archive_external_symlink_is_rejected(self):
        archive = self.root / "sysroot.tgz"
        with tarfile.open(archive, "w:gz") as tar:
            item = tarfile.TarInfo("lib/escape")
            item.type = tarfile.SYMTYPE
            item.linkname = "/etc/passwd"
            tar.addfile(item)
        with self.assertRaises(tarfile.FilterError):
            workload.extract_sysroot(archive, self.root / "extract")

    def test_native_command_failure_retains_exit_and_bounded_diagnostics(self):
        run = workload.Run(self.root / "proof", "build")
        program = "import sys; print('x'*2000000); print('final failure'); sys.exit(7)"
        with self.assertRaisesRegex(RuntimeError, "build failed"):
            run.command("build", [sys.executable, "-c", program])
        self.assertEqual(run.result["commands"][-1]["exitCode"], 7)
        self.assertLessEqual(
            (run.evidence / "build.log").stat().st_size, workload.LIMIT
        )
        self.assertIn("final failure", (run.evidence / "build.tail.log").read_text())
        self.assertEqual(
            json.loads((run.evidence / "result.json").read_text())["status"],
            "incomplete",
        )

    def test_hung_command_is_bounded_and_cannot_report_success(self):
        run = workload.Run(self.root / "proof", "build")
        with self.assertRaises(TimeoutError):
            run.command(
                "hang",
                [sys.executable, "-c", "import time; time.sleep(30)"],
                timeout=0.2,
            )
        self.assertTrue(run.result["commands"][-1]["timedOut"])
        self.assertIsNotNone(run.result["commands"][-1]["exitCode"])

    def test_wrapper_exit_does_not_leave_timed_out_children_running(self):
        run = workload.Run(self.root / "proof", "build")
        pid_file = self.root / "child.pid"
        child = (
            "import os,time; open("
            + repr(str(pid_file))
            + ", 'w').write(str(os.getpid())); time.sleep(30)"
        )
        program = (
            "import subprocess,sys; subprocess.Popen([sys.executable, '-c', "
            + repr(child)
            + "])"
        )
        with self.assertRaises(TimeoutError):
            run.command("orphan", [sys.executable, "-c", program], timeout=0.5)
        pid = int(pid_file.read_text())
        state = subprocess.run(
            ["ps", "-p", str(pid), "-o", "stat="],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertTrue(
            state.returncode != 0 or state.stdout.strip().startswith("Z"), state.stdout
        )
        self.assertTrue(run.result["commands"][-1]["timedOut"])

    def test_permission_failure_for_live_owned_group_is_not_ignored(self):
        process = subprocess.Popen(
            [sys.executable, "-c", "import time; time.sleep(30)"],
            start_new_session=True,
        )
        try:
            with (
                patch("utm_workload.os.killpg", side_effect=PermissionError),
                self.assertRaisesRegex(RuntimeError, "Cannot stop owned process group"),
            ):
                workload.signal_owned_group(process.pid, signal.SIGTERM, {})
        finally:
            process.terminate()
            process.wait(timeout=5)

    def test_native_account_names_and_home_paths_are_scrubbed(self):
        run = workload.Run(self.root / "proof", "build")
        text = "chown " + run.account_name + ":staff " + str(Path.home()) + "/input"
        run.command("identity", [sys.executable, "-c", "print(" + repr(text) + ")"])
        for name in ("identity.log", "identity.tail.log"):
            output = (run.evidence / name).read_text()
            self.assertNotIn(run.account_name, output)
            self.assertNotIn(str(Path.home()), output)
            self.assertIn("<user>:staff <home>/input", output)

    def fixture(self):
        template = self.root / "fixture.utm"
        (template / "Data").mkdir(parents=True)
        (template / "Data/disk.raw").write_bytes(getattr(self, "disk", b"task-disk"))
        config = {
            "Backend": "QEMU",
            "QEMU": {"Hypervisor": True, "AdditionalArguments": []},
            "Information": {
                "UUID": "00000000-0000-0000-0000-000000000001",
                "Name": "fixture",
            },
            "Sharing": {"DirectoryShareMode": "None"},
            "System": {"Architecture": "aarch64"},
            "Drive": [{"ImageName": "disk.raw"}],
        }
        (template / "config.plist").write_bytes(plistlib.dumps(config))
        built = self.root / "built"
        executable = built / "work/UTM.app/Contents/MacOS/UTM"
        executable.parent.mkdir(parents=True)
        executable.write_bytes(b"built-app")
        (executable.parent / "utmctl").write_bytes(b"bundled-cli")
        (built / "evidence").mkdir()
        workload.save(
            built / "evidence/result.json",
            {
                "build": "passed",
                "package": "passed",
                "runId": "build-run",
                "app": {"CFBundleIdentifier": "example.app"},
                "appExecutableSHA256": workload.digest(executable),
            },
        )
        return template, built, config

    def test_git_release_without_submodules_and_uninitialized_gitlink(self):
        repo = self.root / "source"
        subprocess.run(["git", "init", "-q", repo], check=True)
        empty = subprocess.check_output(
            ["git", "-C", repo, "submodule", "status", "--recursive"], text=True
        )
        self.assertEqual(empty, "")
        workload.validate_submodules(empty)
        (repo / ".gitmodules").write_text(
            '[submodule "native"]\npath = native\nurl = https://example.com/native.git\n'
        )
        subprocess.run(["git", "-C", repo, "add", ".gitmodules"], check=True)
        subprocess.run(
            [
                "git",
                "-C",
                repo,
                "update-index",
                "--add",
                "--cacheinfo",
                "160000",
                "1" * 40,
                "native",
            ],
            check=True,
        )
        missing = subprocess.check_output(
            ["git", "-C", repo, "submodule", "status", "--recursive"], text=True
        )
        self.assertTrue(missing.startswith("-"))
        with self.assertRaisesRegex(RuntimeError, "recursive submodules"):
            workload.validate_submodules(missing)

    def test_backend_and_external_fixture_constraints(self):
        _, _, config = self.fixture()
        for change, backend, message in [
            ({"Backend": "Apple"}, "qemu-hvf", "QEMU only"),
            ({"QEMU": {"Hypervisor": False}}, "qemu-hvf", "Hypervisor"),
            ({"Drive": [{"ImageName": "../personal.raw"}]}, "qemu-hvf", "internal"),
            ({"Sharing": {"DirectoryShareMode": "VirtFS"}}, "qemu-hvf", "shared"),
        ]:
            with self.subTest(message=message):
                changed = dict(config, **change)
                with self.assertRaisesRegex(RuntimeError, message):
                    workload.validate_vm(changed, backend)

    def simulate_boot(self, response="fresh", accelerator="hvf", stopped=True):
        template, built, _ = self.fixture()
        original = (template / "config.plist").read_bytes()
        run = workload.Run(self.root / "proof", "boot")
        run.result["hostArchitecture"] = "arm64"
        run.platform = lambda: None
        args = argparse.Namespace(
            build=str(built),
            vm_template=str(template),
            guest_image="public image sha256",
            backend="qemu-hvf",
            runner_kind="physical",
            allow_nested=False,
            timeout=0.1,
        )
        calls = []

        def command(name, argv, **kwargs):
            calls.append((name, [str(x) for x in argv]))
            if name == "existing-app":
                return 1, ""
            if name.startswith("fixture-status"):
                return 0, "stopped\n"
            if name.startswith("guest-challenge"):
                log = run.work / "proof.utm/Data/debug.log"
                log.write_text(
                    'Launching: qemu-system-aarch64 ("-accel", '
                    + accelerator
                    + ', "-m", 1024)\n'
                )
                nonce = run.result["runId"] if response == "fresh" else "stale-run-id"
                return (
                    0,
                    "CRABBOX_GUEST_READY:"
                    + nonce
                    + "\nLinux\n12345678-1234-1234-1234-123456789abc\n",
                )
            if name.startswith("cleanup-status"):
                return 0, "stopped\n" if stopped else "started\n"
            if name == "guest-delete":
                shutil.rmtree(run.work / "proof.utm")
            return 0, ""

        run.command = command
        with patch("utm_workload.time.sleep"):
            if response == "fresh" and accelerator == "hvf" and stopped:
                workload.boot(run, args)
                self.assertEqual(run.result["runtime"], "passed")
                self.assertFalse((run.work / "proof.utm").exists())
            else:
                with self.assertRaises(RuntimeError):
                    workload.boot(run, args)
        self.assertEqual(original, (template / "config.plist").read_bytes())
        task_id = run.result["taskVMUUID"]
        self.assertNotEqual(task_id, "00000000-0000-0000-0000-000000000001")
        for name, argv in calls:
            if name in ("guest-start", "guest-stop", "guest-delete"):
                self.assertIn(task_id, argv)
                self.assertTrue(
                    argv[0].startswith(str(built.resolve() / "work/UTM.app"))
                )
        return run, calls

    def test_self_contained_qcow2_guest_fixture_is_accepted(self):
        header = bytearray(104)
        header[:4] = b"QFI\xfb"
        struct.pack_into(">I", header, 4, 3)
        self.disk = bytes(header)
        self.simulate_boot()

    def test_qcow2_backing_file_is_rejected_before_launch(self):
        header = bytearray(104)
        header[:4] = b"QFI\xfb"
        struct.pack_into(">IQI", header, 4, 3, 104, 15)
        self.disk = bytes(header) + b"/personal/disk"
        template, built, _ = self.fixture()
        run = workload.Run(self.root / "proof", "boot")
        run.platform = lambda: None
        run.result["hostArchitecture"] = "arm64"
        run.command = lambda *a, **kw: self.fail(
            "Backing fixture must fail before launching any app"
        )
        args = argparse.Namespace(
            build=str(built),
            vm_template=str(template),
            guest_image="public sha256",
            backend="qemu-hvf",
            runner_kind="physical",
            allow_nested=False,
        )
        with self.assertRaisesRegex(RuntimeError, "backing chains"):
            workload.boot(run, args)
        self.assertTrue(template.exists())

    def test_fresh_guest_challenge_and_backend_proof_preserve_fixture(self):
        self.simulate_boot()

    def test_zero_exit_with_stale_guest_signal_never_passes(self):
        run, _ = self.simulate_boot(response="stale")
        self.assertNotEqual(run.result["runtime"], "passed")

    def test_guest_boot_without_selected_accelerator_never_passes(self):
        run, _ = self.simulate_boot(accelerator="tcg")
        self.assertNotEqual(run.result["runtime"], "passed")

    def test_cleanup_does_not_delete_unconfirmed_running_copy(self):
        run, calls = self.simulate_boot(stopped=False)
        self.assertTrue((run.work / "proof.utm").exists())
        self.assertFalse(any(name == "guest-delete" for name, _ in calls))


if __name__ == "__main__":
    unittest.main()
