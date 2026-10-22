"""Regression tests reconstructed from the audit's retained test scenarios."""
import os
from pathlib import Path
import select
import signal
import subprocess
import sys
import tempfile
import time
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
from verify import run_command


class VerifyCommandTests(unittest.TestCase):
    def test_completed_command_preserves_exit_status_and_combined_log(self):
        for code in (0, 7):
            with self.subTest(code=code), tempfile.TemporaryFile(mode="w+") as output:
                result = run_command(
                    [sys.executable, "-c",
                     "import sys; print('stdout-marker', flush=True); "
                     "print('stderr-marker', file=sys.stderr, flush=True); "
                     f"sys.exit({code})"],
                    timeout=5, stdout=output, stderr=subprocess.STDOUT)
                self.assertEqual(result.returncode, code)
                output.seek(0)
                self.assertEqual(set(output.read().splitlines()),
                                 {"stdout-marker", "stderr-marker"})

    @unittest.skipUnless(os.name == "posix", "POSIX process-group cleanup")
    def test_timeout_terminates_real_parent_and_child(self):
        reader, writer = os.pipe()
        child_pid = None
        received = b""
        eof = False
        child = ("import os,sys,time; fd=int(sys.argv[1]); "
                 "os.write(fd,(str(os.getpid())+'\\n').encode()); time.sleep(30)")
        parent = ("import subprocess,sys,time; fd=int(sys.argv[1]); "
                  "subprocess.Popen([sys.executable,'-c',sys.argv[2],str(fd)],"
                  "pass_fds=(fd,)); time.sleep(30)")
        try:
            with tempfile.TemporaryFile() as output:
                with self.assertRaises(subprocess.TimeoutExpired):
                    run_command([sys.executable, "-c", parent, str(writer), child],
                                timeout=1, pass_fds=(writer,), stdout=output,
                                stderr=subprocess.STDOUT)
            os.close(writer)
            writer = -1
            deadline = time.monotonic() + 2
            while time.monotonic() < deadline:
                ready, _, _ = select.select([reader], [], [],
                                             max(0, deadline - time.monotonic()))
                if not ready:
                    break
                data = os.read(reader, 4096)
                if not data:
                    eof = True
                    break
                received += data
            if received.strip():
                child_pid = int(received.strip())
            self.assertIsNotNone(child_pid, "the real child must have started")
            self.assertTrue(eof, "child survived verifier timeout and still owns pipe")
        finally:
            if writer >= 0:
                os.close(writer)
            os.close(reader)
            if child_pid is not None and not eof:
                try:
                    os.kill(child_pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass


if __name__ == "__main__":
    unittest.main()
