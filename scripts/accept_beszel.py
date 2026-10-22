#!/usr/bin/env python3
"""Validate a built Topophany CLI against the pinned official Beszel Hub.

Uses disposable, explicitly synthetic records. It never contacts a real agent
or a user-supplied Hub. Network traffic is restricted by construction to the
new Hub process on 127.0.0.1. No passwords or auth tokens are written to logs.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import platform
import secrets
import signal
import socket
import sqlite3
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


ARCHIVE_SHA256 = "c0cbab161f322426228419fbbeab4163dded6464a537cc1e2f92e090892f6368"
SOURCE_COMMIT = "ec96c134660c998f7eb044e349e3715b65733fe1"
SYSTEM_ID = "validationsys01"
OTHER_SYSTEM_ID = "validationsys02"
SAMPLE_ID = "validationrec03"
TOKEN_ENV = "TOPO_BESZEL_LOCAL_VALIDATION_TOKEN"
MAX_RESPONSE_BYTES = 2 * 1024 * 1024
GIB = 1024**3


class AcceptanceFailure(Exception):
    """A safe, locally defined diagnostic; never contains an upstream body."""


def check(condition: bool, message: str) -> None:
    if not condition:
        raise AcceptanceFailure(message)


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def utc_now() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


def pb_time(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat(
        sep=" ", timespec="milliseconds"
    ).replace("+00:00", "Z")


def parse_time(value: str) -> dt.datetime:
    result = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    check(result.tzinfo is not None, "timestamp lacks explicit timezone")
    return result.astimezone(dt.timezone.utc)


def child_environment() -> dict[str, str]:
    # Do not inherit Hub configuration, proxy settings, credentials or hooks.
    return {
        "PATH": os.defpath,
        "LANG": "C.UTF-8",
        "TZ": "UTC",
        "GOMAXPROCS": "2",
    }


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class API:
    def __init__(self, port: int):
        self.origin = f"http://127.0.0.1:{port}"
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}), NoRedirect()
        )

    def request(
        self,
        method: str,
        path: str,
        *,
        token: str = "",
        data: dict | None = None,
        query: dict | None = None,
    ) -> tuple[int, dict]:
        check(path.startswith("/api/"), "invalid local API path")
        url = self.origin + path
        if query:
            url += "?" + urllib.parse.urlencode(query)
        headers = {"Accept": "application/json"}
        if token:
            # PocketBase uses the raw auth token in Authorization.
            headers["Authorization"] = token
        body = None
        if data is not None:
            headers["Content-Type"] = "application/json"
            body = json.dumps(data, separators=(",", ":")).encode("utf-8")
        req = urllib.request.Request(url, data=body, headers=headers, method=method)
        try:
            response = self.opener.open(req, timeout=5)
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
        check(status == 200, f"expected API success for {method} {path}; HTTP {status}")
        return value

    def authenticate(self, collection: str, email: str, password: str) -> dict:
        value = self.ok(
            "POST",
            f"/api/collections/{collection}/auth-with-password",
            data={"identity": email, "password": password},
        )
        check(isinstance(value.get("token"), str) and bool(value["token"]),
              "authentication returned no token")
        return value


def extract_hub(archive: Path, destination: Path) -> Path:
    check(file_sha256(archive) == ARCHIVE_SHA256, "official archive SHA256 mismatch")
    # Extract only the expected regular executable, never archive paths/links.
    with tarfile.open(archive, "r:gz") as bundle:
        matches = [member for member in bundle.getmembers() if member.name == "beszel"]
        check(len(matches) == 1 and matches[0].isfile(), "archive executable missing")
        member = matches[0]
        check(0 < member.size <= 128 * 1024 * 1024, "archive executable size invalid")
        stream = bundle.extractfile(member)
        check(stream is not None, "archive executable unreadable")
        with stream, destination.open("xb") as output:
            while block := stream.read(1024 * 1024):
                output.write(block)
    destination.chmod(0o700)
    return destination


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


def wait_ready(api: API, process: subprocess.Popen) -> None:
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        check(process.poll() is None, "official Hub exited before readiness")
        try:
            status, _ = api.request("GET", "/api/health")
            if status == 200:
                return
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(0.05)
    raise AcceptanceFailure("official Hub readiness timed out")


def list_query(system: str = SYSTEM_ID, page: int = 1) -> dict:
    return {
        "filter": f'system = "{system}" && type = "1m"',
        "sort": "-created,-id",
        "perPage": 1,
        "skipTotal": "true",
        "page": page,
        "fields": "id,system,type,created,stats",
    }


def legacy_stats(cpu: float = 12.5) -> dict:
    # Deliberately keep dp != du/d*100: dp is the upstream percentage.
    return {
        "cpu": cpu,
        "m": 8,
        "mu": 3,
        "mp": 37.5,
        "mb": 1,
        "d": 100,
        "du": 20,
        "dp": 23.25,
        "efs": {"storage": {"d": 200, "du": 20, "r": 0, "w": 0, "rb": 0, "wb": 0}},
    }


def create_fixtures(api: API, admin: str, owner_id: str, readonly_id: str,
                    data_dir: Path) -> dict[str, str]:
    for object_id, users in ((SYSTEM_ID, [readonly_id]), (OTHER_SYSTEM_ID, [owner_id])):
        api.ok("POST", "/api/collections/systems/records", token=admin, data={
            "id": object_id,
            "name": "Synthetic local acceptance fixture",
            "host": "127.0.0.1",
            "port": "1",
            "status": "paused",
            "users": users,
        })
        # The official onRecordCreate hook always sets status to pending.
        # Pause explicitly after creation so no agent scheduling or status
        # transition can change this synthetic fixture during acceptance.
        paused = api.ok("PATCH", f"/api/collections/systems/records/{object_id}",
                        token=admin, data={"status": "paused"})
        check(paused.get("status") == "paused", "synthetic fixture could not be paused")
    now = utc_now()
    fixtures = [
        ("validationrec01", SYSTEM_ID, "1m", 120, 1.5),
        ("validationrec02", SYSTEM_ID, "1m", 45, 2.5),
        (SAMPLE_ID, SYSTEM_ID, "1m", 45, 12.5),
        ("validationrec04", SYSTEM_ID, "10m", 30, 99.5),
        ("validationrec05", OTHER_SYSTEM_ID, "1m", 15, 88.5),
    ]
    times = {}
    for record_id, object_id, interval, age, cpu in fixtures:
        api.ok("POST", "/api/collections/system_stats/records", token=admin, data={
            "id": record_id, "system": object_id, "type": interval,
            "stats": legacy_stats(cpu),
        })
        times[record_id] = pb_time(now - dt.timedelta(seconds=age))
    # PocketBase created is an autodate. Control it ONLY in this disposable DB
    # to test deterministic tied timestamps and newer non-1m/other-system rows.
    db_path = data_dir / "data.db"
    check(db_path.is_file(), "private Hub database missing")
    connection = sqlite3.connect(db_path, timeout=5)
    try:
        with connection:
            for record_id, created in times.items():
                cursor = connection.execute(
                    'UPDATE system_stats SET created = ? WHERE id = ?',
                    (created, record_id),
                )
                check(cursor.rowcount == 1, "synthetic timestamp update failed")
    finally:
        connection.close()
    return times


def verify_api(api: API, admin: str, readonly: str, times: dict[str, str]) -> dict:
    route = "/api/collections/system_stats/records"
    responses = {}
    for role, token in (("superuser", admin), ("readonly", readonly)):
        value = api.ok("GET", route, token=token, query=list_query())
        check(value.get("page") == 1 and value.get("perPage") == 1,
              "API pagination parameters not preserved")
        check(value.get("totalItems") == -1 and value.get("totalPages") == -1,
              "API did not skip the historical count query")
        check(len(value.get("items", [])) == 1, "API latest query returned wrong row count")
        selected = value["items"][0]
        check(selected.get("id") == SAMPLE_ID and selected.get("system") == SYSTEM_ID,
              "API sort did not select the latest tied 1m sample")
        check(selected.get("type") == "1m", "API selected rollup sample")
        check(selected.get("created") == times[SAMPLE_ID], "API changed fixture time")
        check(selected.get("stats") == legacy_stats(), "API changed synthetic stats")
        parse_time(selected["created"])
        responses[role] = value

    # A separate counted read verifies the fixture history without requiring
    # the production latest-sample request to count it on every collection.
    counted_query = dict(list_query(), skipTotal="false")
    counted = api.ok("GET", route, token=readonly, query=counted_query)
    check(counted.get("totalItems") == 3 and counted.get("totalPages") == 3,
          "API filter/count included wrong system or rollup")
    check(counted.get("items") == responses["readonly"]["items"],
          "count-free query changed latest-sample selection")

    page2 = api.ok("GET", route, token=readonly, query=list_query(page=2))
    check([v["id"] for v in page2["items"]] == ["validationrec02"],
          "API second-page tie ordering invalid")
    hidden = api.ok("GET", route, token=readonly, query=list_query(OTHER_SYSTEM_ID))
    anonymous = api.ok("GET", route, query=list_query())
    for value in (hidden, anonymous):
        check(value.get("items") == [] and value.get("totalItems") == -1
              and value.get("totalPages") == -1, "listRule failed to hide records")

    denied_stats_view, _ = api.request("GET", f"{route}/{SAMPLE_ID}", token=readonly)
    check(denied_stats_view == 403, "regular stats getOne unexpectedly allowed")
    denied_create, _ = api.request("POST", "/api/collections/users/records",
                                   token=readonly, data={})
    check(denied_create == 403, "readonly account unexpectedly allowed user creation")
    denied_system_view, _ = api.request(
        "GET", f"/api/collections/systems/records/{OTHER_SYSTEM_ID}", token=readonly
    )
    check(denied_system_view == 404, "unassigned system view unexpectedly allowed")

    system_route = f"/api/collections/systems/records/{SYSTEM_ID}"
    before = api.ok("GET", system_route, token=readonly,
                    query={"fields": "id,status,updated"})
    time.sleep(0.01)  # PocketBase dates have millisecond precision.
    api.ok("PATCH", system_route, token=admin,
           data={"name": "Renamed synthetic fixture: metadata only"})
    after = api.ok("GET", system_route, token=readonly,
                   query={"fields": "id,status,updated"})
    check(before["status"] == "paused" and after["status"] == "paused",
          "synthetic system status changed unexpectedly")
    check(parse_time(after["updated"]) > parse_time(before["updated"]),
          "metadata rename did not update systems.updated")
    return {
        "latest_1m_responses": responses,
        "counted_reference": counted,
        "page2_sample_id": page2["items"][0]["id"],
        "unassigned_list": hidden,
        "anonymous_list": anonymous,
        "readonly_stats_get_one_status": denied_stats_view,
        "readonly_create_user_status": denied_create,
        "readonly_unassigned_system_status": denied_system_view,
        "system_before_metadata_rename": before,
        "system_after_metadata_rename": after,
        "metadata_rename_changes_updated": True,
    }


def topology(origin: str) -> dict:
    return {
        "schema": 1,
        "project": "beszel-official-acceptance",
        "nodes": [{"id": "fixture", "name": "Synthetic Beszel fixture", "kind": "host"}],
        "endpoints": [], "services": [], "paths": [],
        "observers": [{"id": "validation", "node": "fixture",
                       "context": "isolated official Hub acceptance"}],
        "sources": [{
            "id": "beszel-local", "adapter": "beszel", "observer": "validation",
            "target": origin, "credential_env": TOKEN_ENV,
            "interval_seconds": 60, "timeout_seconds": 8, "stale_seconds": 180,
            "max_response_bytes": 1048576, "max_pages": 2,
            "max_objects": 1, "max_report_bytes": 65536,
        }],
        "checks": [{
            "id": f"fixture-{kind}", "name": f"Synthetic {kind}",
            "observer": "validation", "kind": "external",
            "external": {
                "source": "beszel-local", "object": SYSTEM_ID, "report": kind,
                "node": "fixture", "max_age_seconds": 180,
                **({"primary_disk": "root"} if kind == "resource" else {}),
            },
        } for kind in ("resource", "status")],
    }


def check_no_secrets(raw: str, secret_values: list[str]) -> None:
    check(not any(value and value in raw for value in secret_values),
          "credential appeared in captured output")


def run_cli(topo: Path, config: Path, readonly: str, secret_values: list[str]) -> list[dict]:
    environment = child_environment()
    environment[TOKEN_ENV] = readonly
    result = subprocess.run(
        [str(topo), "once", "--config", str(config), "--observer", "validation"],
        env=environment, cwd=config.parent, capture_output=True, text=True,
        timeout=20, check=False,
    )
    check_no_secrets(result.stdout + result.stderr, secret_values)
    check(result.returncode == 0,
          f"production CLI returned {result.returncode}: {result.stderr.strip()[:512]}")
    check(len(result.stdout) <= MAX_RESPONSE_BYTES, "CLI output exceeds limit")
    observations = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
    check(len(observations) == 2, "production CLI did not emit exactly two observations")
    check({item.get("check") for item in observations} == {"fixture-resource", "fixture-status"},
          "production CLI observation identities invalid")
    for item in observations:
        check(item.get("outcome") == "ok", "production CLI collection failed")
        check(item.get("evidence") == "reported", "CLI evidence is not reported")
        check(item.get("connectivity") is False, "CLI invented connectivity evidence")
        external = item.get("external", {})
        check(external.get("contact") is True, "CLI did not record successful Hub contact")
        check(external.get("report", {}).get("object") == SYSTEM_ID,
              "CLI returned data for wrong system")
    return observations


def verify_cli(observations: list[dict], expected_time: str) -> None:
    by_check = {item["check"]: item["external"]["report"] for item in observations}
    report = by_check["fixture-resource"]
    check(report.get("sample_id") == SAMPLE_ID, "CLI chose wrong latest raw sample")
    check(report.get("time_basis") == "upstream_sample", "CLI sample time basis invalid")
    check(parse_time(report["observed_at"]) == parse_time(expected_time),
          "CLI did not preserve Hub sample timestamp")
    resource = report["resource"]
    check(resource["cpu"].get("percent") == 12.5, "CLI CPU percentage incorrect")
    memory = resource["memory"]
    check(memory.get("total_bytes") == 8 * GIB and memory.get("used_bytes") == 3 * GIB,
          "CLI legacy GiB memory conversion incorrect")
    check(memory.get("percent") == 37.5, "CLI memory percentage incorrect")
    check("available_bytes" not in memory, "CLI invented available memory")
    disks = {disk["id"]: disk for disk in resource["disks"]}
    fs_id = "fs-" + hashlib.sha256(b"storage").hexdigest()
    check(set(disks) == {"root", fs_id}, "CLI filesystem identities incorrect")
    root = disks["root"]
    check(root.get("total_bytes") == 100 * GIB and root.get("used_bytes") == 20 * GIB,
          "CLI root disk GiB conversion incorrect")
    check(root.get("percent") == 23.25, "CLI overwrote authoritative root percentage")
    extra = disks[fs_id]
    check(extra.get("total_bytes") == 200 * GIB and extra.get("used_bytes") == 20 * GIB,
          "CLI extra filesystem conversion incorrect")
    check(extra.get("percent") == 10, "CLI extra filesystem percentage incorrect")
    for disk in disks.values():
        check("free_bytes" not in disk and "path" not in disk,
              "CLI invented free space or filesystem path")
    status = by_check["fixture-status"]
    check(status.get("status", {}).get("status") == "unknown", "CLI paused status incorrect")
    check(status.get("time_basis") == "unknown", "CLI invented status sample time")
    check("observed_at" not in status, "CLI used metadata update as telemetry time")


def run(args: argparse.Namespace) -> dict:
    check(platform.system() == "Linux" and platform.machine() in {"x86_64", "amd64"},
          "this pinned archive requires Linux amd64")
    topo = args.topo.resolve(strict=True)
    archive = args.archive.resolve(strict=True)
    check(topo.is_file() and os.access(topo, os.X_OK), "Topophany CLI is not executable")
    topo_hash_before = file_sha256(topo)
    started = utc_now().isoformat()
    with tempfile.TemporaryDirectory(prefix="topophany-beszel-acceptance-") as directory:
        work = Path(directory)
        work.chmod(0o700)
        binary = extract_hub(archive, work / "beszel")
        data_dir = work / "db"
        data_dir.mkdir(mode=0o700)
        environment = child_environment()
        email = "acceptance-owner@example.invalid"
        password = secrets.token_urlsafe(32)
        reader_password = secrets.token_urlsafe(32)
        environment.update({
            "BESZEL_HUB_USER_EMAIL": email,
            "BESZEL_HUB_USER_PASSWORD": password,
            "BESZEL_HUB_DISABLE_SSH": "true",
            "BESZEL_HUB_SHARE_ALL_SYSTEMS": "false",
        })
        version = subprocess.run([str(binary), "--version"], capture_output=True,
                                 text=True, env=child_environment(), cwd=work,
                                 timeout=5, check=False)
        check(version.returncode == 0 and "0.21.0" in version.stdout,
              "official Hub version check failed")
        port = reserve_port()
        api = API(port)
        process = subprocess.Popen(
            [str(binary), "serve", "--http", f"127.0.0.1:{port}", "--dir", str(data_dir)],
            cwd=work, env=environment, stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        try:
            wait_ready(api, process)
            admin_auth = api.authenticate("_superusers", email, password)
            owner_auth = api.authenticate("users", email, password)
            admin = admin_auth["token"]
            reader = api.ok("POST", "/api/collections/users/records", token=admin, data={
                "id": "validationusr01", "email": "acceptance-reader@example.invalid",
                "password": reader_password, "passwordConfirm": reader_password,
                "verified": True, "role": "readonly",
            })
            reader_auth = api.authenticate("users", "acceptance-reader@example.invalid",
                                           reader_password)
            readonly = reader_auth["token"]
            secret_values = [password, reader_password, admin, owner_auth["token"], readonly]
            check(reader.get("role") == "readonly", "synthetic account role was not readonly")
            times = create_fixtures(api, admin, owner_auth["record"]["id"], reader["id"], data_dir)
            api_result = verify_api(api, admin, readonly, times)
            config_path = work / "topology.json"
            config_path.write_text(json.dumps(topology(api.origin)), encoding="utf-8")
            config_path.chmod(0o600)
            observations = run_cli(topo, config_path, readonly, secret_values)
            verify_cli(observations, times[SAMPLE_ID])
            topo_hash_after = file_sha256(topo)
            check(topo_hash_after == topo_hash_before, "CLI binary changed during acceptance")
            result = {
                "schema": 1,
                "passed": True,
                "started_at": started,
                "finished_at": utc_now().isoformat(),
                "binary": "Official Beszel v0.21.0 linux amd64 release",
                "archive_sha256": ARCHIVE_SHA256,
                "hub_binary_sha256": file_sha256(binary),
                "source_commit": SOURCE_COMMIT,
                "topo_binary_sha256_before": topo_hash_before,
                "topo_binary_sha256_after": topo_hash_after,
                "harness_sha256": file_sha256(Path(__file__).resolve()),
                "fixture_kind": "Synthetic API-created records, locally controlled Hub timestamps; no real Agent",
                "loopback_only": True,
                "raw_authorization_token": True,
                "regular_readonly_account_supported": True,
                "query": list_query(),
                "api": api_result,
                "production_cli_observations": observations,
                "limitations": [
                    "Uses the official v0.21.0 Hub binary; fixtures are synthetic, not Agent-collected telemetry.",
                    "created is the Hub record time, not an independently reported Agent measurement time.",
                    "Does not execute the forward mem[4] Hub commit; forward protocol is separately source-verified and unit-tested.",
                    "The latest-sample query skips historical counts; record reads remain point-in-time observations.",
                ],
            }
            check_no_secrets(json.dumps(result), secret_values)
            return result
        finally:
            stop_process(process)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--topo", type=Path, required=True, help="Built production Topophany CLI")
    parser.add_argument("--archive", type=Path, required=True, help="Pinned official v0.21.0 Linux amd64 release archive (SHA256 verified)")
    parser.add_argument("--output", type=Path, required=True, help="Write this run's sanitized JSON evidence")
    args = parser.parse_args()
    # SIGINT/SIGTERM also pass through finally blocks to stop our Hub process.
    def interrupted(signum, frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    try:
        result = run(args)
        output = args.output.resolve()
        output.parent.mkdir(parents=True, exist_ok=True)
        content = (json.dumps(result, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
        # Publish only completed, sanitized evidence, using an atomic replacement.
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
        print("Acceptance interrupted; local Hub cleaned up.", file=sys.stderr)
        return 130
    except Exception as error:
        # Never print exceptions that may contain auth responses or secret data.
        print(f"Acceptance failed ({type(error).__name__}); no raw response logged.",
              file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
