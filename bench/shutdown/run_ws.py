"""Real gateway SIGTERM/restart probe for isolated synthetic WebSockets.

Use the same --output directory as run.py, containing new-api, harness and
prebuilt wsprobe (Linux) / wsprobe.exe (host). SQLite operations reuse run.py's
Linux helper, keeping WAL locking in the same kernel as the gateway.
"""
import argparse
import json
import subprocess
import time
import urllib.request
import uuid
from pathlib import Path

import run as fixture


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    root = args.output.resolve()
    fixture.FIXTURE_ROOT = root
    for name in ("new-api", "harness", "wsprobe", "wsprobe.exe"):
        if not (root / name).is_file():
            parser.error(f"missing prebuilt {root/name}")
    stage = root / ("websocket-" + uuid.uuid4().hex[:8])
    stage.mkdir()
    prefix = "newapi-ws-" + uuid.uuid4().hex[:10]
    network = prefix + "-network"
    resources = []
    child = None
    gateway = None
    app_name = prefix + "-app"
    report = {"database": str(stage / "gateway.db"), "mock_upstream": True}

    def stop(name):
        started = time.monotonic()
        fixture.docker("kill", "--signal", "TERM", name)
        return {"exit_code": int(fixture.docker("wait", name)), "seconds": round(time.monotonic()-started, 3)}

    def probe(name, normal=True):
        path = stage / (name + ".json")
        command = [str(root / "wsprobe.exe"), "-mode", "probe", "-target", gateway,
                   "-token", fixture.TOKEN, "-listen", f"127.0.0.1:{fixture.free_port()}", "-report", str(path)]
        if normal:
            command.append("-normal")
        result = subprocess.run(command, capture_output=True, text=True, timeout=25)
        value = json.loads(path.read_text()) if path.exists() else {"error": result.stderr}
        value["probe_exit_code"] = result.returncode
        return value

    def http_request():
        body = json.dumps({"model": "shutdown-normal", "messages": [{"role": "user", "content": "fixture"}], "max_tokens": 64}).encode()
        req = urllib.request.Request(gateway + "/v1/chat/completions", body, {"Authorization": "Bearer " + fixture.TOKEN, "Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=10) as response:
            value = json.load(response)
            return {"passed": response.status == 200 and value["choices"][0]["finish_reason"] == "stop" and value["usage"]["total_tokens"] == 150,
                    "request_id": response.headers.get("X-Oneapi-Request-Id", response.headers.get("X-Request-Id", ""))}

    try:
        fixture.docker("network", "create", "--label", "task=" + prefix, network)
        for suffix, image, command in (("redis", "redis:7-alpine", ["redis-server", "--save", "", "--appendonly", "no"]),
                                       ("mock", "alpine:3.22", ["/opt/wsprobe", "-mode", "upstream"])):
            name = prefix + "-" + suffix
            cmd = ["run", "-d", "--name", name, "--label", "task=" + prefix, "--network", network, "--network-alias", suffix]
            if suffix == "mock":
                cmd += ["-p", "127.0.0.1::8080", "-v", str(root / "wsprobe") + ":/opt/wsprobe:ro"]
            resources.append(name)
            fixture.docker(*cmd, image, *command)
            if suffix == "mock":
                upstream_port = int(fixture.docker("port", name, "8080/tcp").rsplit(":", 1)[1])
        fixture.wait_ready(f"http://127.0.0.1:{upstream_port}/stats")
        env = {"PORT": "3000", "SQLITE_PATH": "/data/gateway.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate",
               "SQL_DSN": "", "LOG_SQL_DSN": "", "REDIS_CONN_STRING": "redis://redis:6379/0",
               "SESSION_SECRET": "shutdown-isolated-fixture-stable-secret", "CRYPTO_SECRET": "shutdown-isolated-fixture-stable-secret",
               "MEMORY_CACHE_ENABLED": "true", "BATCH_UPDATE_ENABLED": "true", "BATCH_UPDATE_INTERVAL": "5",
               "SHUTDOWN_TIMEOUT_SECONDS": "3", "GLOBAL_API_RATE_LIMIT_ENABLE": "false", "GLOBAL_API_USER_RATE_LIMIT_ENABLE": "false",
               "GLOBAL_WEB_RATE_LIMIT_ENABLE": "false", "UPDATE_TASK": "false", "TASK_PLUGIN_ENABLED": "false", "LOG_LANGUAGE": "en"}
        cmd = ["run", "-d", "--name", app_name, "--label", "task=" + prefix, "--network", network,
               "-p", "127.0.0.1::3000", "-v", str(root / "new-api") + ":/opt/new-api:ro", "-v", str(stage) + ":/data", "-w", "/data"]
        for key, value in env.items():
            cmd += ["-e", key + "=" + value]
        resources.append(app_name)
        fixture.docker(*cmd, "alpine:3.22", "/opt/new-api")
        gateway_port = int(fixture.docker("port", app_name, "3000/tcp").rsplit(":", 1)[1])
        gateway = f"http://127.0.0.1:{gateway_port}"
        fixture.wait_ready(gateway + "/api/status")
        stop(app_name)
        fixture.seed(stage / "gateway.db")
        fixture.docker("start", app_name)
        gateway_port = int(fixture.docker("port", app_name, "3000/tcp").rsplit(":", 1)[1])
        gateway = f"http://127.0.0.1:{gateway_port}"
        fixture.wait_ready(gateway + "/api/status")
        report["baseline_normal_close"] = probe("baseline")
        report["http_before_stop"] = http_request()
        control_port = fixture.free_port()
        hold_path = stage / "held.json"
        child = subprocess.Popen([str(root / "wsprobe.exe"), "-mode", "probe", "-target", gateway, "-token", fixture.TOKEN,
                                  "-listen", f"127.0.0.1:{control_port}", "-report", str(hold_path)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        deadline = time.monotonic() + 10
        held = None
        while time.monotonic() < deadline:
            try:
                held = fixture.get(f"http://127.0.0.1:{control_port}/stats")
                if held["exchanged"]:
                    break
            except Exception:
                pass
            time.sleep(0.05)
        if not held or not held["exchanged"]:
            raise RuntimeError("held WS did not complete a bidirectional exchange")
        report["stop_with_websocket"] = stop(app_name)
        child.wait(timeout=25)
        report["held_websocket"] = json.loads(hold_path.read_text())
        report["held_websocket"]["probe_exit_code"] = child.returncode
        child = None
        before = fixture.snapshot(stage / "gateway.db")
        fixture.docker("start", app_name)
        gateway_port = int(fixture.docker("port", app_name, "3000/tcp").rsplit(":", 1)[1])
        gateway = f"http://127.0.0.1:{gateway_port}"
        fixture.wait_ready(gateway + "/api/status")
        report["after_restart_websocket"] = probe("restart")
        report["http_after_restart"] = http_request()
        report["final_stop"] = stop(app_name)
        after = fixture.snapshot(stage / "gateway.db")
        report["old_records_unchanged"] = (before["consume_records"] == after["consume_records"][:len(before["consume_records"])]
                                            and before["cost_records"] == after["cost_records"][:len(before["cost_records"])])
        report["post_restart_http_logged_once"] = after["consume_ids"].count(report["http_after_restart"]["request_id"]) == 1
        report["persisted_before_stop_http"] = before["consume_ids"].count(report["http_before_stop"]["request_id"]) == 1
        report["snapshots"] = {"before_restart": before, "after_restart": after}
        report["held_ws_gracefully_closed"] = report["held_websocket"].get("normal_close", False)
        report["passed_restart"] = (report["stop_with_websocket"]["exit_code"] == report["final_stop"]["exit_code"] == 0
                                    and all(report[key].get("exchanged") for key in ("baseline_normal_close", "after_restart_websocket"))
                                    and all(report[key]["passed"] for key in ("http_before_stop", "http_after_restart"))
                                    and report["old_records_unchanged"] and report["post_restart_http_logged_once"] and report["persisted_before_stop_http"])
    finally:
        if child and child.poll() is None:
            child.terminate()
            child.wait(timeout=5)
        for name in reversed(resources):
            try:
                (stage / (name.rsplit("-", 1)[-1] + ".log")).write_text(fixture.docker("logs", name, check=False), encoding="utf-8")
            finally:
                fixture.docker("rm", "-f", name, check=False)
        fixture.docker("network", "rm", network, check=False)
        (stage / "report.json").write_text(json.dumps(report, indent=2), encoding="utf-8")
    print(json.dumps({"report": str(stage / "report.json"), "passed_restart": report["passed_restart"],
                      "held_ws_gracefully_closed": report["held_ws_gracefully_closed"], "held_close_code": report["held_websocket"].get("close_code")}, indent=2))
    if not report["passed_restart"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
