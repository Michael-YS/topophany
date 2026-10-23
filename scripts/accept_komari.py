#!/usr/bin/env python3
"""Check a built Topophany CLI against the pinned official Komari 1.6.0 binary.

Starts a disposable server on literal loopback, installs a synthetic account,
creates synthetic nodes and submits Agent v2 reports to that server only.
Production adapter reads are exercised by the real CLI. Credentials, database
files and server logs are never published, and the server is stopped on exit.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import re
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid


BINARY_SHA256 = "81657c0f616edb64a247d9f7a1628be104e344e6cd6eec6da82d43050a3d56e5"
SOURCE_COMMIT = "1b76b7ad5bc3de6529bb7f0577069b4ac241fec3"
TOKEN_ENV = "TOPO_KOMARI_LOCAL_ACCEPTANCE_KEY"
MAX_RESPONSE_BYTES = 2 * 1024 * 1024
AGENT_TOTAL = 9007199254740993  # Odd integer beyond float64's exact range.
WIRE_TOTAL = 9007199254740992   # 1.6.0 rounds during its Agent RPC any -> Report conversion.


class AcceptanceFailure(Exception):
    """A locally defined diagnostic that does not contain upstream bodies."""


def check(condition: bool, message: str) -> None:
    if not condition:
        raise AcceptanceFailure(message)


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat()


def sample_instant(raw: str) -> tuple[dt.datetime, int]:
    """Compare the official UTC wire format without losing submicrosecond time."""
    check(isinstance(raw, str), "sample timestamp is not a string")
    match = re.fullmatch(
        r"([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})"
        r"(?:\.([0-9]{1,9}))?Z", raw,
    )
    check(match is not None, "sample timestamp is not the official UTC format")
    try:
        second = dt.datetime.fromisoformat(match[1] + "+00:00")
    except ValueError:
        raise AcceptanceFailure("sample timestamp has an invalid calendar value") from None
    nanosecond = int((match[2] or "").ljust(9, "0"))
    return second, nanosecond


def child_environment() -> dict[str, str]:
    # No inherited credentials, source configuration, proxies or Go hooks.
    return {"PATH": os.defpath, "LANG": "C.UTF-8", "TZ": "UTC", "GOMAXPROCS": "2"}


def no_secrets(raw: str, values: list[str]) -> None:
    check(not any(value and value in raw for value in values),
          "credential appeared in captured output")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class API:
    def __init__(self, port: int):
        self.origin = f"http://127.0.0.1:{port}"
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}), NoRedirect()
        )

    def request(self, method: str, path: str, *, key: str = "",
                session: str = "", data: dict | None = None) -> tuple[int, dict]:
        check(path.startswith("/api/") and "?" not in path and "#" not in path,
              "invalid local API path")
        headers = {"Accept": "application/json"}
        if key:
            headers["Authorization"] = "Bearer " + key
        if session:
            headers["Cookie"] = "session_token=" + session
        body = None
        if data is not None:
            headers["Content-Type"] = "application/json"
            body = json.dumps(data, separators=(",", ":")).encode("utf-8")
        request = urllib.request.Request(self.origin + path, data=body,
                                         headers=headers, method=method)
        try:
            response = self.opener.open(request, timeout=5)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            status = response.status
            raw = response.read(MAX_RESPONSE_BYTES + 1)
        check(len(raw) <= MAX_RESPONSE_BYTES, "API response limit exceeded")
        value = json.loads(raw)
        check(isinstance(value, dict), "API response is not an object")
        return status, value

    def ok(self, method: str, path: str, **kwargs) -> dict:
        status, value = self.request(method, path, **kwargs)
        check(status == 200, f"local API {method} {path} failed with HTTP {status}")
        check(value.get("status") == "success" or value.get("jsonrpc") == "2.0",
              "local API response did not indicate success")
        check("error" not in value, "local RPC returned an error")
        return value


def reserve_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def stop_process(process: subprocess.Popen) -> None:
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def wait_ready(api: API, process: subprocess.Popen, path: str) -> dict:
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        check(process.poll() is None, "official server exited before readiness")
        try:
            status, value = api.request("GET", path)
            if status == 200 and value.get("status") == "success":
                return value
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(0.05)
    raise AcceptanceFailure("official server readiness timed out")


def install(api: API, process: subprocess.Popen, work: Path,
            password: str, api_key: str) -> str:
    wait_ready(api, process, "/api/install/status")
    api.ok("POST", "/api/install/complete", data={
        "username": "acceptance-owner", "password": password,
        "sitename": "Synthetic local acceptance",
        "metric_dsn": "file:" + str(work / "metrics.db") + "?mode=rwc",
    })
    version = wait_ready(api, process, "/api/version")["data"]
    check(version.get("version") == "1.6.0", "official binary version mismatch")
    upstream_hash = version.get("hash", "")
    check(len(upstream_hash) >= 7 and SOURCE_COMMIT.startswith(upstream_hash),
          "official binary source commit mismatch")
    auth = api.ok("POST", "/api/login", data={
        "username": "acceptance-owner", "password": password,
    })
    session = auth.get("data", {}).get("set-cookie", {}).get("session_token")
    check(isinstance(session, str) and bool(session), "login returned no session")
    api.ok("POST", "/api/admin/settings/", session=session, data={
        "api_key": api_key, "private_site": True, "geo_ip_enabled": False,
        "geo_ip_provider": "empty", "notification_enabled": False,
    })
    return session


def create_node(api: API, key: str, name: str) -> tuple[str, str]:
    value = api.ok("POST", "/api/admin/client/add", key=key, data={"name": name})
    object_id, token = value.get("uuid"), value.get("token")
    check(isinstance(object_id, str) and str(uuid.UUID(object_id)) == object_id,
          "created node UUID is not canonical")
    check(isinstance(token, str) and bool(token), "created node has no Agent token")
    return object_id, token


def submit(api: API, token: str, cpu: float) -> None:
    # 1.6.0 reads Agent credentials from the body/query, whereas administrator
    # API keys use Bearer. Keep this local fixture token out of URL/log strings.
    api.ok("POST", "/api/clients/v2/rpc", data={
        "token": token,
        "jsonrpc": "2.0", "method": "agent.report", "id": 1,
        "params": {"report": {
            # Both values must be replaced by the real server at ingestion.
            "uuid": "00000000-0000-0000-0000-000000000000",
            "updated_at": "2000-01-01T00:00:00Z",
            "cpu": {"usage": cpu},
            "ram": {"total": AGENT_TOTAL, "used": 4503599627370497},
            "disk": {"total": 107374182400, "used": 32212254720},
            "uptime": 86400,
        }},
    })


def recent(api: API, key: str, object_id: str) -> list[dict]:
    value = api.ok("GET", f"/api/recent/{object_id}", key=key)
    reports = value.get("data")
    check(isinstance(reports, list), "recent response is not an array")
    check(all(item.get("uuid") == object_id for item in reports),
          "recent reports have the wrong node identity")
    return reports


def topology(origin: str, objects: list[str], authenticated: bool) -> dict:
    return {
        "schema": 1, "project": "komari-official-acceptance",
        "nodes": [{"id": f"fixture-{index}", "name": "Synthetic Komari fixture",
                   "kind": "host", "resource_checks": [f"fixture-{index}"]}
                  for index in range(len(objects))],
        "endpoints": [], "services": [], "paths": [],
        "observers": [{"id": "validation", "node": "fixture-0",
                       "context": "isolated official Komari acceptance"}],
        "sources": [{
            "id": "komari-local", "adapter": "komari", "observer": "validation",
            "target": origin, **({"credential_env": TOKEN_ENV} if authenticated else {}),
            "interval_seconds": 15, "timeout_seconds": 8, "stale_seconds": 60,
            "max_response_bytes": 1048576, "max_pages": len(objects),
            "max_objects": len(objects), "max_report_bytes": 65536,
        }],
        "checks": [{
            "id": f"fixture-{index}", "name": "Synthetic resource",
            "observer": "validation", "kind": "external",
            "external": {"source": "komari-local", "object": object_id,
                         "report": "resource", "node": f"fixture-{index}",
                         "max_age_seconds": 60, "primary_disk": "aggregate"},
        } for index, object_id in enumerate(objects)],
    }


def run_cli(topo: Path, work: Path, api: API, objects: list[str], key: str,
            secret_values: list[str]) -> list[dict]:
    config = work / "topology.json"
    config.write_text(json.dumps(topology(api.origin, objects, bool(key))), encoding="utf-8")
    config.chmod(0o600)
    environment = child_environment()
    if key:
        environment[TOKEN_ENV] = key
    result = subprocess.run(
        [str(topo), "once", "--config", str(config), "--observer", "validation"],
        env=environment, cwd=work, capture_output=True, text=True,
        timeout=20, check=False,
    )
    no_secrets(result.stdout + result.stderr, secret_values)
    check(result.returncode == 0, "production CLI command failed")
    check(len(result.stdout) <= MAX_RESPONSE_BYTES, "CLI output limit exceeded")
    observations = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
    check(len(observations) == len(objects), "CLI observation count incorrect")
    check({item.get("check") for item in observations} == {
        f"fixture-{index}" for index in range(len(objects))
    }, "CLI observation bindings incorrect")
    return sorted(observations, key=lambda item: item["check"])


def verify_direct(topo: Path, work: Path, api: API, objects: list[str], key: str,
                  secret_values: list[str], *, anonymous: bool = False) -> None:
    root = work / ("direct-public" if anonymous else "direct-private")
    environment = child_environment()
    environment[TOKEN_ENV] = key

    def command(arguments: list[str], request: dict | None = None,
                expected_exit: int = 0) -> dict:
        result = subprocess.run(
            [str(topo), *arguments, "--direct-root", str(root)],
            input=json.dumps(request) if request is not None else None,
            env=environment, cwd=work, capture_output=True, text=True,
            timeout=20, check=False,
        )
        no_secrets(result.stdout + result.stderr, secret_values)
        check(result.returncode == expected_exit, "direct CLI command failed")
        check(len(result.stdout) <= MAX_RESPONSE_BYTES, "direct output limit exceeded")
        return json.loads(result.stdout)

    request = {
        "schema": 1, "profile": "acceptance", "base_revision": "",
        "source": {"id": "km-official", "name": "Official Komari",
                   "adapter": "komari", "url": api.origin, "interval_seconds": 30},
        "auth": {"mode": "anonymous"} if anonymous else {"mode": "env", "name": TOKEN_ENV},
        "selected_objects": objects,
    }
    inspected = command(["sources", "inspect", "--json-stdin"], request)
    check(inspected.get("ok") is True, "direct discovery failed")
    check({node["id"] for node in inspected["data"]["systems"]} == set(objects),
          "direct discovery returned the wrong visible nodes")
    check(not root.exists(), "direct inspect wrote private state")
    if not anonymous:
        environment[TOKEN_ENV] = "synthetic-invalid-api-key"
        denied = command(["sources", "inspect", "--json-stdin"], request, expected_exit=1)
        check(denied.get("error", {}).get("code") == "auth_required",
              "direct discovery accepted an invalid key or fell back to public access")
        environment[TOKEN_ENV] = key
    saved = command(["sources", "save", "--json-stdin"], request)
    check(saved.get("ok") is True, "direct profile save failed")
    snapshot = command(["--source", "direct", "--profile", "acceptance", "--json"])
    check(snapshot.get("sources_status", {}).get("state") == "ready",
          "direct resources did not become current")
    reports = [item.get("report", {}) for item in snapshot.get("external", [])]
    check(len(reports) == len(objects), "direct snapshot lost selected resources")
    for report in reports:
        resource = report.get("resource", {})
        check(resource.get("memory", {}).get("total_bytes") == WIRE_TOTAL,
              "direct resource byte counters changed")
        check(resource.get("disks", [{}])[0].get("id") == "aggregate",
              "direct disk selection is not aggregate")
    command(["status", "--source", "direct", "--profile", "acceptance", "--json"])
    command(["show", "servers", "--source", "direct", "--profile", "acceptance", "--json"])
    command(["sources", "remove", "--json-stdin"], {
        "schema": 1, "profile": "acceptance", "source_id": "km-official",
        "base_revision": saved["data"]["profile_revision"],
    })


def verify_report(observation: dict, object_id: str, sample: dict, cpu: float) -> dict:
    check(observation.get("outcome") == "ok", "production collection failed")
    check(observation.get("evidence") == "reported"
          and observation.get("connectivity") is False,
          "CLI invented connectivity evidence")
    external = observation.get("external", {})
    check(external.get("contact") is True, "CLI did not record successful source contact")
    report = external.get("report", {})
    check(report.get("object") == object_id, "CLI report object is incorrect")
    check(report.get("time_basis") == "upstream_sample", "CLI time basis incorrect")
    # Both official server and Go report marshaler use RFC3339Nano in UTC;
    # exact equality detects truncation to microseconds or milliseconds.
    check(report.get("observed_at") == sample["updated_at"],
          "CLI did not preserve the precise server receive timestamp")
    check(isinstance(report.get("sample_id"), str) and bool(report["sample_id"]),
          "CLI did not emit a stable sample identity")
    resource = report.get("resource", {})
    check(resource.get("cpu", {}).get("percent") == cpu, "CLI CPU percentage incorrect")
    memory = resource.get("memory", {})
    check(memory.get("total_bytes") == WIRE_TOTAL
          and memory.get("used_bytes") == 4503599627370497,
          "CLI did not preserve the integer memory counters actually served")
    check(math.isclose(memory.get("percent", -1), 50.0, rel_tol=1e-12),
          "CLI memory percentage incorrect")
    disks = resource.get("disks", [])
    check(len(disks) == 1 and disks[0].get("id") == "aggregate",
          "CLI aggregate disk identity incorrect")
    disk = disks[0]
    check(disk.get("total_bytes") == 107374182400
          and disk.get("used_bytes") == 32212254720 and disk.get("percent") == 30.0,
          "CLI aggregate disk bytes or percentage incorrect")
    check("path" not in disk, "CLI invented an aggregate disk mount point")
    return report


def verify_failed(observations: list[dict], *, expected_code: str) -> None:
    check(bool(observations), "failed collection produced no observations")
    check(all(item.get("code") == expected_code for item in observations),
          "source failure did not have the expected cause")
    check(all(item.get("external", {}).get("report") is None for item in observations),
          "failed collection leaked a successful prefix")
    check(all(item.get("outcome") == "error"
              and item.get("evidence") == "reported"
              and item.get("connectivity") is False
              and item.get("external", {}).get("contact") is False
              for item in observations),
          "source failure became target evidence or refreshed source contact")


def run(args: argparse.Namespace) -> dict:
    check(platform.system() == "Linux" and platform.machine() in {"x86_64", "amd64"},
          "this pinned binary requires Linux amd64")
    topo = args.topo.resolve(strict=True)
    supplied = args.binary.resolve(strict=True)
    check(topo.is_file() and os.access(topo, os.X_OK), "Topophany CLI is not executable")
    check(supplied.is_file() and file_sha256(supplied) == BINARY_SHA256,
          "official Komari binary SHA256 mismatch")
    topo_hash = file_sha256(topo)
    started = utc_now()
    with tempfile.TemporaryDirectory(prefix="topophany-komari-acceptance-") as directory:
        work = Path(directory)
        work.chmod(0o700)
        binary = work / "komari"
        shutil.copyfile(supplied, binary)
        check(file_sha256(binary) == BINARY_SHA256, "copied binary SHA256 mismatch")
        binary.chmod(0o700)
        port = reserve_port()
        api = API(port)
        password, key = "Aa1-" + secrets.token_urlsafe(32), secrets.token_urlsafe(32)
        secret_values = [password, key]
        process = subprocess.Popen(
            [str(binary), "server", "--listen", f"127.0.0.1:{port}"],
            cwd=work, env=child_environment(), stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        try:
            secret_values.append(install(api, process, work, password, key))
            objects, agent_tokens = [], []
            for name in ("Synthetic positive CPU", "Synthetic zero CPU"):
                object_id, token = create_node(api, key, name)
                objects.append(object_id)
                agent_tokens.append(token)
                secret_values.append(token)
            submit(api, agent_tokens[0], 1.25)
            submit(api, agent_tokens[0], 12.5)
            submit(api, agent_tokens[1], 0.0)
            samples = [recent(api, key, object_id) for object_id in objects]
            check(len(samples[0]) == 2 and len(samples[1]) == 1,
                  "official recent window omitted synthetic samples")
            check(sample_instant(samples[0][0]["updated_at"])
                  < sample_instant(samples[0][1]["updated_at"]),
                  "official recent samples are not in time order")
            check(all(not item["updated_at"].startswith("2000-")
                      for items in samples for item in items),
                  "official server trusted the supplied Agent timestamp")
            check("usage" not in samples[1][0]["cpu"],
                  "official zero CPU omission differs from pinned schema")
            check(all(item["ram"]["total"] == WIRE_TOTAL
                      for items in samples for item in items),
                  "official Agent integer rounding differs from pinned behavior")
            baseline = run_cli(topo, work, api, objects, key, secret_values)
            first_reports = [verify_report(item, object_id, items[-1], cpu)
                             for item, object_id, items, cpu in
                             zip(baseline, objects, samples, (12.5, 0.0), strict=True)]
            verify_direct(topo, work, api, objects, key, secret_values)
            repeated = run_cli(topo, work, api, objects, key, secret_values)
            for item, previous, object_id, items, cpu in zip(
                    repeated, first_reports, objects, samples, (12.5, 0.0), strict=True):
                current = verify_report(item, object_id, items[-1], cpu)
                check(current["sample_id"] == previous["sample_id"]
                      and current["observed_at"] == previous["observed_at"],
                      "polling the same sample changed identity or freshness")
            denied_status, _ = api.request("GET", f"/api/recent/{objects[0]}",
                                           key="synthetic-invalid-api-key")
            check(denied_status == 401, "private site accepted an invalid key")
            verify_failed(run_cli(topo, work, api, objects,
                                  "synthetic-invalid-api-key", secret_values),
                          expected_code="komari_auth")
            # A public first node plus an inaccessible second node exercises
            # atomic failure after the adapter has already read valid data.
            api.ok("POST", "/api/admin/settings/", key=key, data={"private_site": False})
            api.ok("POST", f"/api/admin/client/{objects[1]}/edit", key=key,
                   data={"hidden": True})
            anonymous = run_cli(topo, work, api, objects[:1], "", secret_values)
            verify_report(anonymous[0], objects[0], samples[0][-1], 12.5)
            verify_direct(topo, work, api, objects[:1], key, secret_values, anonymous=True)
            public_invalid = run_cli(topo, work, api, objects[:1],
                                     "synthetic-invalid-api-key", secret_values)
            verify_report(public_invalid[0], objects[0], samples[0][-1], 12.5)
            hidden_authenticated = run_cli(topo, work, api, objects[1:], key, secret_values)
            verify_report(hidden_authenticated[0], objects[1], samples[1][-1], 0.0)
            hidden_status, _ = api.request("GET", f"/api/recent/{objects[1]}")
            check(hidden_status == 400, "anonymous hidden-node read was not rejected")
            hidden_invalid_status, _ = api.request("GET", f"/api/recent/{objects[1]}",
                                                  key="synthetic-invalid-api-key")
            check(hidden_invalid_status == 400, "hidden node accepted an invalid key")
            verify_failed(run_cli(topo, work, api, objects[1:],
                                  "synthetic-invalid-api-key", secret_values),
                          expected_code="komari_http")
            verify_failed(run_cli(topo, work, api, objects, "", secret_values),
                          expected_code="komari_http")
            empty_id, empty_token = create_node(api, key, "Synthetic node without reports")
            secret_values.append(empty_token)
            check(recent(api, key, empty_id) == [], "unused node has unexpected samples")
            empty = run_cli(topo, work, api, [empty_id], key, secret_values)
            check(empty[0].get("external", {}).get("report") is None,
                  "CLI fabricated a report for an empty recent window")
            check(empty[0].get("outcome") == "error"
                  and empty[0].get("code") == "external_object_missing"
                  and empty[0].get("evidence") == "reported"
                  and empty[0].get("connectivity") is False
                  and empty[0].get("external", {}).get("contact") is True,
                  "empty recent window lost successful contact or created target evidence")
            check(file_sha256(topo) == topo_hash, "CLI binary changed during acceptance")
            result = {
                "schema": 1, "passed": True, "started_at": started,
                "finished_at": utc_now(), "binary": "Official Komari 1.6.0 linux amd64",
                "binary_sha256": BINARY_SHA256, "source_commit": SOURCE_COMMIT,
                "topo_binary_sha256_before": topo_hash,
                "topo_binary_sha256_after": file_sha256(topo),
                "harness_sha256": file_sha256(Path(__file__).resolve()),
                "fixture_kind": "Synthetic nodes and Agent v2 POST reports on a disposable loopback server",
                "checks": {
                    "official_release_digest": True, "bearer_private_site": True,
                    "two_objects": True, "latest_receive_timestamp_preserved": True,
                    "wire_integer_counters_preserved": True, "zero_cpu_omitempty": True,
                    "same_sample_does_not_refresh": True, "anonymous_public_node": True,
                    "direct_authenticated_discovery_and_resources": True,
                    "direct_invalid_key_has_no_guest_fallback": True,
                    "direct_anonymous_discovery_and_resources": True,
                    "invalid_key_rejected_on_private_site": True,
                    "public_site_read_cannot_validate_supplied_key": True,
                    "hidden_node_readable_with_valid_key": True,
                    "hidden_node_invalid_key_rejected": True,
                    "hidden_node_rejected_for_guest": True,
                    "later_request_failure_is_atomic": True, "empty_window_has_no_report": True,
                },
                "upstream_integer_precision": {
                    "agent_total_submitted": AGENT_TOTAL,
                    "recent_total_returned": WIRE_TOTAL,
                    "reason": "Komari 1.6.0 Agent RPC decodes through float64 before constructing its int64 report",
                },
                "production_cli_observations": baseline,
                "limitations": [
                    "Uses the official 1.6.0 server binary with synthetic Agent reports, not a real Agent process.",
                    "Odd integers above 2^53 are already rounded by this server's Agent ingestion; adapter precision for such wire integers is covered by unit fixtures.",
                    "Does not wait for the one-minute recent-window expiration; offline aging is covered by the integration suite.",
                    "The legacy recent endpoint is listed for future compatibility cleanup upstream; other versions are not certified.",
                ],
            }
            no_secrets(json.dumps(result), secret_values)
            return result
        finally:
            stop_process(process)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--topo", type=Path, required=True, help="Built production Topophany CLI")
    parser.add_argument("--binary", type=Path, required=True,
                        help="Official Komari 1.6.0 Linux amd64 binary (SHA256 verified)")
    parser.add_argument("--output", type=Path, required=True,
                        help="Write completed, sanitized JSON evidence")
    args = parser.parse_args()

    def interrupted(signum, frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, interrupted)
    try:
        result = run(args)
        output = args.output.resolve()
        output.parent.mkdir(parents=True, exist_ok=True)
        content = (json.dumps(result, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
        with tempfile.NamedTemporaryFile(prefix=".acceptance-result-", dir=output.parent,
                                         delete=False) as handle:
            temporary = Path(handle.name)
            try:
                handle.write(content)
                handle.flush()
                os.fsync(handle.fileno())
            except BaseException:
                temporary.unlink(missing_ok=True)
                raise
        try:
            temporary.replace(output)
        finally:
            temporary.unlink(missing_ok=True)
        print(json.dumps({"passed": True, "result": str(output),
                          "topo_binary_sha256": result["topo_binary_sha256_after"]}))
        return 0
    except AcceptanceFailure as error:
        print(f"Acceptance failed: {error}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("Acceptance interrupted; local server cleaned up.", file=sys.stderr)
        return 130
    except Exception as error:
        print(f"Acceptance failed ({type(error).__name__}); no raw response logged.",
              file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
