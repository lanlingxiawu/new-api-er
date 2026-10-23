"""Run a bounded local load stage and verify asynchronous accounting drains.

Requires the dedicated gateway/MySQL/PostgreSQL fixtures documented in README.
Credentials stay in the private state file and are never printed or reported.
"""
import argparse
import json
import subprocess
import threading
import time
import urllib.request
from pathlib import Path
from urllib.parse import urlparse


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--state", required=True)
    parser.add_argument("--loadgen", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--name", required=True)
    parser.add_argument("--rps", type=float, required=True)
    parser.add_argument("--seconds", type=int, default=60)
    parser.add_argument("--concurrency", type=int, default=128)
    parser.add_argument("--gateway", default="http://127.0.0.1:23000")
    parser.add_argument("--mysql-database", choices=["gateway", "gateway_final"], default="gateway")
    parser.add_argument("--postgres-database", choices=["gatewaylog", "gatewaylog_final"], default="gatewaylog")
    args = parser.parse_args()
    if urlparse(args.gateway).hostname not in ("127.0.0.1", "localhost"):
        parser.error("only an isolated loopback gateway is allowed")
    out = Path(args.output).resolve()
    state = json.loads(Path(args.state).read_text(encoding="utf-8"))
    base = args.gateway.rstrip("/")

    def api(path, data=None, token=None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        req = urllib.request.Request(base + path, headers=headers, data=json.dumps(data).encode() if data is not None else None)
        with urllib.request.urlopen(req, timeout=10) as resp:
            result = json.load(resp)
        if not result.get("success"):
            raise RuntimeError("gateway status request failed")
        return result["data"]

    admin = api("/api/user/login", {"username": state["username"], "password": state["password"]})["access_token"]

    def snapshot():
        # Fixed names/ports belong only to the isolated fixtures, never the
        # user's existing database services on their normal ports.
        sql = "SELECT quota,used_quota,request_count FROM users WHERE id=1; SELECT remain_quota,used_quota FROM tokens WHERE id=1; SELECT used_quota FROM channels WHERE id=1; SELECT COUNT(*),COALESCE(SUM(revenue_quota),0) FROM consumption_costs;"
        raw = subprocess.check_output([r"C:\Program Files\MySQL\MySQL Server 8.4\bin\mysql.exe", "--protocol=TCP", "-h", "127.0.0.1", "-P", "23306", "-u", "root", "-BN", args.mysql_database, "-e", sql], text=True)
        keys = ["user_quota", "user_used", "user_requests", "token_remain", "token_used", "channel_used", "cost_records", "cost_revenue"]
        values = [int(v) for v in raw.split()]
        result = dict(zip(keys, values, strict=True))
        raw = subprocess.check_output([r"C:\Program Files\PostgreSQL\18\bin\psql.exe", "-h", "127.0.0.1", "-p", "25432", "-U", "validation", args.postgres_database, "-At", "-c", "SELECT count(*),COALESCE(sum(quota),0) FROM logs WHERE type=2;"], text=True)
        result["log_records"], result["log_quota"] = map(int, raw.strip().split("|"))
        return result

    samples = []
    stopped = threading.Event()

    def sample():
        while not stopped.is_set():
            row = {"at": time.time()}
            try:
                row["relay"] = api("/api/admin/system/relay-log-pipeline/status", token=admin)
                row["ledger"] = api("/api/admin/system/ledger-pipeline/status", token=admin)
                perf = api("/api/performance/stats", token=admin)
                row["memory"] = perf["memory_stats"]
                row["system"] = perf["system_status"]
                row["body"] = perf["cache_stats"]
            except Exception as exc:
                row["error"] = type(exc).__name__
            samples.append(row)
            stopped.wait(2)

    before = snapshot()
    sampler = threading.Thread(target=sample, daemon=True)
    sampler.start()
    report = out / (args.name + ".json")
    cmd = [str(Path(args.loadgen).resolve()), "-url", base + "/v1/chat/completions", "-token", state["api_token"], "-model", "gpt-4o-mini", "-c", str(args.concurrency), "-rps", str(args.rps), "-d", str(args.seconds) + "s", "-stream-ratio", "0.5", "-timeout", "15s", "-report", str(report)]
    try:
        with (out / (args.name + ".log")).open("w", encoding="utf-8") as log:
            subprocess.run(cmd, stdout=log, stderr=subprocess.STDOUT, check=True)
        load = json.loads(report.read_text(encoding="utf-8"))
        drain_start = time.monotonic()
        consistent = False
        while time.monotonic() - drain_start < 30:
            time.sleep(1)
            after = snapshot()
            delta = {k: after[k] - v for k, v in before.items()}
            counts = [delta[k] for k in ("user_requests", "cost_records", "log_records")]
            amounts = [delta[k] for k in ("user_used", "token_used", "channel_used", "cost_revenue", "log_quota")]
            amounts += [-delta["user_quota"], -delta["token_remain"]]
            consistent = all(n == load["success"] for n in counts) and len(set(amounts)) == 1
            if consistent:
                break
        ledger = api("/api/admin/system/ledger-pipeline/status", token=admin)
        relay = api("/api/admin/system/relay-log-pipeline/status", token=admin)
        evidence = {"before": before, "after": after, "delta": delta, "consistent": consistent, "drain_seconds": round(time.monotonic()-drain_start, 3), "relay_after": relay, "ledger_after": ledger}
        (out / (args.name + "-accounting.json")).write_text(json.dumps(evidence, indent=2), encoding="utf-8")
        reached_target = args.rps == 0 or load["throughput_rps"] >= args.rps * 0.95
        print(json.dumps({"stage": args.name, "rps": load["throughput_rps"], "target_reached": reached_target, "success": load["success"], "failed": load["failed"], "accounting_consistent": consistent}), flush=True)
    finally:
        stopped.set()
        sampler.join(timeout=12)
        (out / (args.name + "-telemetry.json")).write_text(json.dumps(samples, indent=2), encoding="utf-8")
    return 0 if consistent and reached_target and load["failed"] == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
