"""Opt-in real Linux gateway stop/restart verification with synthetic AI traffic.

Requires Docker and prebuilt Linux new-api/harness plus host harness.exe binaries.
All state is created below --output; Docker resources use a unique task prefix.
The repository, .env, real databases, and real upstreams are never mounted.
"""
import argparse
from contextlib import closing
import hashlib
import json
import random
import shutil
import socket
import sqlite3
import subprocess
import sys
import time
import urllib.request
import uuid
from pathlib import Path


QUOTA = 1_000_000_000
TOKEN = "sk-shutdownFixtureOnlyNotARealCredential"
MODELS = ["shutdown-normal", "shutdown-long"]
FIXTURE_ROOT = None
SQL_FIXTURE = None
CURRENT_DATABASE = None
TASK_PREFIX = ""


def connect(path):
    return SQL_FIXTURE.connect(CURRENT_DATABASE) if SQL_FIXTURE else SQLiteFixture(path)


class FixtureCursor:
    def __init__(self, rows):
        self.rows = rows

    def fetchone(self):
        return self.rows[0] if self.rows else None

    def __iter__(self):
        return iter(self.rows)


class SQLiteFixture:
    """Use Linux file locks for SQLite, including while the gateway is running.

    Windows sqlite3 must never open the live Docker WAL: the two kernels cannot
    coordinate locks, and the host may unlink a WAL still in use by Linux.
    """
    def __init__(self, path):
        self.path = Path(path)

    def execute(self, statement, arguments=()):
        output = docker("run", "--rm", "--label", "task="+TASK_PREFIX, "--network", "none", "-v", str(FIXTURE_ROOT/"harness")+":/opt/harness:ro", "-v", str(self.path.parent)+":/data", "alpine:3.22", "/opt/harness", "-mode", "database", "-database", "/data/"+self.path.name, "-query", statement, "-args", json.dumps(arguments))
        return FixtureCursor(json.loads(output))

    def commit(self):
        pass

    def close(self):
        pass


def docker(*args, check=True):
    result = subprocess.run(["docker", *map(str, args)], capture_output=True, text=True, encoding="utf-8", errors="replace", timeout=90)
    if check and result.returncode:
        raise RuntimeError(f"docker {args[0]} failed: {result.stderr}")
    return (result.stdout + result.stderr if args[0] == "logs" else result.stdout).strip()


def get(url, timeout=2):
    with urllib.request.urlopen(url, timeout=timeout) as response:
        return json.load(response)


def free_port():
    # Keep listeners outside Windows' ephemeral client-port range. Otherwise
    # load requests after TERM can acquire the just-freed listener port as their
    # source port and prevent immediate rebinding of the restarted gateway.
    for _ in range(100):
        port = random.SystemRandom().randint(20000, 40000)
        try:
            with socket.socket() as sock:
                sock.bind(("127.0.0.1", port))
                return port
        except OSError:
            continue
    raise RuntimeError("no loopback fixture port is available")


def wait_ready(url, seconds=45):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            return get(url)
        except Exception:
            time.sleep(0.15)
    raise RuntimeError(f"local fixture did not become ready: {url}")


def seed(db_path):
    with closing(connect(db_path)) as db:
        db.execute('INSERT INTO users(id,username,password,role,status,quota,used_quota,request_count,"group",group_ratios,aff_code,inviter_id,setting,auth_version,profile_version) VALUES(1,?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
                   ("stopfixture", "unused-fixture-hash", 1, 1, QUOTA, 0, 0, "default", "{}", "stopfixture", 0, "{}", 1, 1))
        db.execute('INSERT INTO tokens(id,user_id,"key",status,name,expired_time,remain_quota,unlimited_quota,used_quota,"group") VALUES(1,1,?,1,?, -1,?,?,0,?)', (TOKEN.removeprefix("sk-"), "shutdown-fixture", QUOTA, False, "default"))
        channel_info_value = 'CAST(? AS JSON)' if SQL_FIXTURE and SQL_FIXTURE.kind == "postgres" else '?'
        db.execute('INSERT INTO channels(id,type,"key",status,name,base_url,models,"group",used_quota,priority,weight,auto_ban,channel_info) VALUES(1,1,?,1,?,?,?,?,0,0,1,0,'+channel_info_value+')',
                   ("synthetic", "shutdown-fixture", "http://mock:8080", ",".join(MODELS), "default", "{}"))
        for model in MODELS:
            db.execute('INSERT INTO abilities("group",model,channel_id,enabled,priority,weight) VALUES(?,?,1,?,0,1)', ("default", model, True))
        options = {
            "ModelRatio": json.dumps(dict.fromkeys(MODELS, 1)),
            "CompletionRatio": json.dumps(dict.fromkeys(MODELS, 1)),
            "GroupRatio": '{"default":1}', "GroupGroupRatio": "{}", "ModelPrice": "{}",
            "billing_setting.billing_mode": json.dumps(dict.fromkeys(MODELS, "ratio")),
            "LogConsumeEnabled": "true", "DataExportEnabled": "false",
            "relay_log_pipeline_setting.enabled": "true",
            "business_stats_circuit_breaker_setting.manual_disabled": "false",
        }
        if SQL_FIXTURE:
            # Leave room for the probe and background work within the isolated
            # DB server's 300-connection limit instead of using a 1000-conn pool.
            options.update({"db_pool_setting.max_open_conns":"100", "db_pool_setting.max_idle_conns":"20"})
        for key, value in options.items():
            db.execute('INSERT OR REPLACE INTO options("key",value) VALUES(?,?)', (key, value))
        db.commit()


def snapshot(db_path):
    with closing(connect(db_path)) as db:
        values = db.execute("SELECT quota,used_quota,request_count FROM users WHERE id=1").fetchone()
        result = dict(zip(("user_quota", "user_used", "user_requests"), values))
        result.update(zip(("token_remain", "token_used"), db.execute("SELECT remain_quota,used_quota FROM tokens WHERE id=1").fetchone()))
        result["channel_used"] = db.execute("SELECT used_quota FROM channels WHERE id=1").fetchone()[0]
        result.update(zip(("log_count", "log_quota", "log_distinct_ids"), db.execute("SELECT COUNT(*),COALESCE(SUM(quota),0),COUNT(DISTINCT request_id) FROM logs WHERE type=2 AND user_id=1").fetchone()))
        result.update(zip(("cost_count", "cost_revenue", "cost_quota"), db.execute("SELECT COUNT(*),COALESCE(SUM(revenue_quota),0),COALESCE(SUM(cost_quota),0) FROM consumption_costs WHERE user_id=1").fetchone()))
        result["consume_ids"] = [row[0] for row in db.execute("SELECT request_id FROM logs WHERE type=2 AND user_id=1 ORDER BY request_id")]
        result["missing_costs"] = db.execute("SELECT COUNT(*) FROM logs l LEFT JOIN consumption_costs c ON c.log_id=l.id WHERE l.type=2 AND l.user_id=1 AND c.log_id IS NULL").fetchone()[0]
        result["invalid_cost_links"] = db.execute("SELECT COUNT(*) FROM consumption_costs c LEFT JOIN logs l ON c.log_id=l.id WHERE c.user_id=1 AND (l.id IS NULL OR l.user_id<>1 OR l.type<>2 OR c.revenue_quota<>l.quota OR c.cost_quota<>l.quota)").fetchone()[0]
        result["duplicate_cost_links"] = db.execute("SELECT COUNT(*) FROM (SELECT log_id FROM consumption_costs WHERE user_id=1 GROUP BY log_id HAVING COUNT(*)>1) AS duplicate_costs").fetchone()[0]
        result["consume_records"] = list(db.execute("SELECT id,request_id,quota FROM logs WHERE type=2 AND user_id=1 ORDER BY id"))
        result["cost_records"] = list(db.execute("SELECT id,log_id,revenue_quota,cost_quota FROM consumption_costs WHERE user_id=1 ORDER BY id"))
        return result


def invariants(before, after, load, strict=True):
    complete = [row for row in load["requests"] if row["outcome"] == "complete"]
    ids = {row["request_id"] for row in complete}
    delivered_ids = ids | {row.get("request_id", "") for row in load["requests"] if row.get("received_content")}
    new_ids = set(after["consume_ids"]) - set(before["consume_ids"])
    missing_terminal = delivered_ids - new_ids
    delta = {key: after[key]-value for key, value in before.items() if isinstance(value, int)}
    expected = len(complete)*150
    new_records = after["consume_records"][len(before["consume_records"]):]
    logged = dict((row[1], row[2]) for row in new_records)
    accounting_quota = expected if strict else delta["log_quota"]
    result = {
        "delta": delta, "complete_http_requests": len(complete), "expected_quota": expected,
        "delivered_response_requests": len(delivered_ids),
        "delivered_responses_without_terminal_logs": len(missing_terminal),
        "missing_terminal_log_examples": sorted(missing_terminal)[:5],
        "delivered_responses_have_terminal_logs": not missing_terminal and "" not in delivered_ids,
        "request_ids_match": (ids == new_ids if strict else ids.issubset(new_ids)) and "" not in ids and not (new_ids & set(before["consume_ids"])),
        "wallet_token_channel_match": all(delta[key] == accounting_quota for key in ("user_used", "token_used", "channel_used")) and -delta["user_quota"] == accounting_quota and -delta["token_remain"] == accounting_quota,
        "consume_logs_match": delta["log_distinct_ids"] == delta["log_count"] and all(logged.get(key) == 150 for key in ids) and (not strict or (delta["log_count"] == len(complete) and delta["log_quota"] == expected)),
        "request_count_matches": delta["user_requests"] == delta["log_count"],
        "costs_match": delta["cost_count"] == delta["log_count"] and delta["cost_revenue"] == delta["log_quota"] and delta["cost_quota"] == delta["log_quota"] and delta["missing_costs"] == 0 and delta["invalid_cost_links"] == 0 and delta["duplicate_cost_links"] == 0,
        "old_records_unchanged": before["consume_records"] == after["consume_records"][:len(before["consume_records"])] and before["cost_records"] == after["cost_records"][:len(before["cost_records"])],
        "negative_charges": sum(row[2] < 0 for row in new_records),
    }
    result["passed"] = all(value for value in result.values() if isinstance(value, bool)) and result["negative_charges"] == 0
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--cases", default="normal500,normal1667,timeout,kill,batch-off,queued-logs")
    parser.add_argument("--database", choices=("sqlite", "mysql", "postgres"), default="sqlite")
    args = parser.parse_args()
    root = args.output.resolve()
    global FIXTURE_ROOT, SQL_FIXTURE, CURRENT_DATABASE, TASK_PREFIX
    FIXTURE_ROOT = root
    for binary in ("new-api", "harness", "harness.exe"):
        if not (root/binary).is_file():
            parser.error(f"missing prebuilt {root/binary}")
    prefix = "newapi-shutdown-" + uuid.uuid4().hex[:10]
    TASK_PREFIX = prefix
    network = prefix + "-network"
    resources = []
    children = []
    results = []
    started_wall = time.time()
    binary_sha256 = hashlib.file_digest((root/"new-api").open("rb"), "sha256").hexdigest()
    build_info = subprocess.run(["go", "version", "-m", str(root/"new-api")], capture_output=True, text=True, encoding="utf-8", errors="replace", timeout=10).stdout
    (root/"buildinfo.txt").write_text(build_info, encoding="utf-8")

    def launch(name, directory, batch=True, timeout=8, redis_db=0, port=None):
        port = port or free_port()
        env = {"PORT":"3000", "SQLITE_PATH":"/data/gateway.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate", "SQL_DSN":"", "LOG_SQL_DSN":"", "REDIS_CONN_STRING":f"redis://redis:6379/{redis_db}", "SESSION_SECRET":"shutdown-isolated-fixture-stable-secret", "CRYPTO_SECRET":"shutdown-isolated-fixture-stable-secret", "MEMORY_CACHE_ENABLED":"true", "BATCH_UPDATE_ENABLED":str(batch).lower(), "BATCH_UPDATE_INTERVAL":"5", "SHUTDOWN_TIMEOUT_SECONDS":str(timeout), "GLOBAL_API_RATE_LIMIT_ENABLE":"false", "GLOBAL_API_USER_RATE_LIMIT_ENABLE":"false", "GLOBAL_WEB_RATE_LIMIT_ENABLE":"false", "UPDATE_TASK":"false", "TASK_PLUGIN_ENABLED":"false", "LOG_LANGUAGE":"en", "RELAY_MAX_IDLE_CONNS":"3000", "RELAY_MAX_IDLE_CONNS_PER_HOST":"3000"}
        if SQL_FIXTURE:
            env["SQL_DSN"] = SQL_FIXTURE.dsn(CURRENT_DATABASE)
        cmd = ["run", "-d", "--name", name, "--label", "task="+prefix, "--network", network, "-p", f"127.0.0.1:{port}:3000", "-v", str(root/"new-api")+":/opt/new-api:ro", "-v", str(directory)+":/data", "-w", "/data"]
        for key,value in env.items(): cmd.extend(("-e", key+"="+value))
        cmd.extend(("alpine:3.22", "/opt/new-api"))
        resources.append(name)
        docker(*cmd)
        wait_ready(f"http://127.0.0.1:{port}/api/status")
        return f"http://127.0.0.1:{port}"

    def stop(name, signal="TERM"):
        start = time.monotonic()
        docker("kill", "--signal", signal, name)
        exit_code = int(docker("wait", name))
        return {"exit_code": exit_code, "stop_seconds": round(time.monotonic()-start,3)}

    def cache_snapshot(index, stored):
        value = docker("exec", prefix+"-redis", "redis-cli", "-n", index, "HGET", "user:1", "Quota")
        token_counters = []
        for key in docker("exec", prefix+"-redis", "redis-cli", "-n", index, "--scan", "--pattern", "token:*").splitlines():
            if docker("exec", prefix+"-redis", "redis-cli", "-n", index, "TYPE", key) == "hash":
                raw = docker("exec", prefix+"-redis", "redis-cli", "-n", index, "HMGET", key, "RemainQuota", "UsedQuota").splitlines()
                token_counters.append([int(item) for item in raw])
        quota = int(value) if value else None
        return {"user_quota":quota, "token_counters":token_counters, "matches_database":quota == stored["user_quota"] and token_counters == [[stored["token_remain"],stored["token_used"]]]}

    try:
        # Docker Desktop does not expose published ports on internal networks.
        # The fixture uses a private bridge and only synthetic local endpoints.
        docker("network", "create", "--label", "task="+prefix, network)
        if args.database != "sqlite":
            from db import SQLFixture
            SQL_FIXTURE = SQLFixture(args.database, prefix, network, resources, docker)
        for suffix, image, command in (("redis", "redis:7-alpine", ["redis-server", "--save", "", "--appendonly", "no"]), ("mock", "alpine:3.22", ["/opt/harness", "-mode", "upstream"])):
            name = prefix+"-"+suffix
            cmd = ["run", "-d", "--name", name, "--label", "task="+prefix, "--network", network, "--network-alias", suffix]
            if suffix == "mock":
                mock_port = free_port()
                cmd.extend(("-p",f"127.0.0.1:{mock_port}:8080","-v",str(root/"harness")+":/opt/harness:ro"))
            docker(*cmd, image, *command)
            resources.append(name)
        mock_url = f"http://127.0.0.1:{mock_port}"
        wait_ready(mock_url+"/stats")
        bootstrap = root/"bootstrap"
        bootstrap.mkdir(exist_ok=True)
        if not SQL_FIXTURE:
            name = prefix+"-bootstrap"
            launch(name, bootstrap)
            stop(name)
            seed(bootstrap/"gateway.db")

        for index, case in enumerate(args.cases.split(","), start=1):
            stage = root/case
            stage.mkdir(exist_ok=False)
            if SQL_FIXTURE:
                CURRENT_DATABASE = "fixture"+str(index)
                SQL_FIXTURE.create_database(CURRENT_DATABASE)
                schema = prefix+"-"+case+"-schema"
                launch(schema, stage, redis_db=index)
                stop(schema)
                seed(stage/"gateway.db")
            else:
                # The source is stopped; no Windows process opens a live WAL.
                with closing(sqlite3.connect(bootstrap/"gateway.db")) as source:
                    with closing(sqlite3.connect(stage/"gateway.db")) as destination:
                        source.backup(destination)
            if case == "queued-logs":
                with closing(connect(stage/"gateway.db")) as db:
                    db.execute('INSERT OR REPLACE INTO options("key",value) VALUES(?,?)', ("relay_log_pipeline_setting.flush_interval_ms", "60000"))
                    db.commit()
            if case.startswith("preconsume"):
                with closing(connect(stage/"gateway.db")) as db:
                    db.execute('INSERT OR REPLACE INTO options("key",value) VALUES(?,?)', ("quota_setting.trust_quota_usd", "0"))
                    db.commit()
            batch = case != "batch-off"
            rate = 1667 if case == "normal1667" else 500
            long = case.endswith("timeout")
            name = prefix+"-"+case
            gateway = launch(name, stage, batch=batch, timeout=2 if long else 8, redis_db=index)
            original_gateway = gateway
            before = snapshot(stage/"gateway.db")
            load_report = stage/"load.json"
            control_port = free_port()
            command = [str(root/"harness.exe"),"-mode","load","-listen",f"127.0.0.1:{control_port}","-target",gateway,"-token",TOKEN,"-rps",str(rate),"-duration","8s","-mixed","-report",str(load_report),"-model","shutdown-long" if long else "shutdown-normal"]
            with (stage/"load.stdout").open("w", encoding="utf-8") as stdout:
                child = subprocess.Popen(command, stdout=stdout, stderr=subprocess.STDOUT)
                children.append(child)
                control = f"http://127.0.0.1:{control_port}"
                wait_ready(control+"/stats")
                time.sleep(4)
                inflight = get(mock_url+"/stats")
                if inflight["active"] <= 0: raise RuntimeError("stop must occur with active synthetic upstream streams")
                signal_state = get(control+"/signal")
                stopping = stop(name, "KILL" if case == "kill" else "TERM")
                child.wait(timeout=50)
                if child.returncode: raise RuntimeError("load harness failed")
            load = json.loads(load_report.read_text())
            after = snapshot(stage/"gateway.db")
            logs = docker("logs", "--timestamps", name)
            (stage/"gateway.log").write_text(logs, encoding="utf-8")
            result = {"case":case,"database":args.database,"batch_enabled":batch,"force_preconsume":case.startswith("preconsume"),"http_drain_budget_seconds":2 if long else 8,"requested_rps":rate,"actual_started_rps_before_signal":round(signal_state["started"]/(signal_state["elapsed_ms"]/1000),2),"signal_state":signal_state,"upstream_at_signal":inflight,"stop":stopping,"http":{k:v for k,v in load.items() if k!="requests"},"accounting":invariants(before,after,load,strict=not long and case != "kill"),"shutdown_timeout_logged":"context deadline exceeded" in logs,"shutdown_flush_logged":"business stat buffers" in logs,"cache_after_stop":cache_snapshot(index,after)}
            result["preconsume_log_examples"] = [line for line in logs.splitlines() if "needs pre-consume" in line][:3]
            result["preconsume_observed"] = bool(result["preconsume_log_examples"])

            # Restart uses exactly the same data and Redis DB, without seed/reset.
            restarted = prefix+"-"+case+"-restart"
            gateway = launch(restarted,stage,batch=batch,timeout=8,redis_db=index,port=int(original_gateway.rsplit(":",1)[1]))
            ready_snapshot = snapshot(stage/"gateway.db")
            restart_report = stage/"restart-load.json"
            subprocess.run([str(root/"harness.exe"),"-mode","load","-listen",f"127.0.0.1:{free_port()}","-target",gateway,"-token",TOKEN,"-rps","50","-duration","2s","-mixed","-report",str(restart_report)],stdout=subprocess.DEVNULL,check=True,timeout=50)
            restart_stop = stop(restarted)
            restart_load = json.loads(restart_report.read_text())
            final = snapshot(stage/"gateway.db")
            result["restart"] = {"same_endpoint":gateway==original_gateway,"unchanged_at_ready":ready_snapshot==after,"http":{k:v for k,v in restart_load.items() if k!="requests"},"stop":restart_stop,"accounting":invariants(after,final,restart_load)}
            result["restart"]["cache_after_stop"] = cache_snapshot(index,final)
            result["restart"]["passed"] = restart_stop["exit_code"] == 0 and ready_snapshot == after and restart_load["complete"] == 100 and restart_load["http_error"] == 0 and restart_load["interrupted"] == 0 and restart_load["refused"] == 0 and result["restart"]["accounting"]["passed"] and result["restart"]["cache_after_stop"]["matches_database"]
            result["load_valid"] = signal_state["dropped_at_capacity"] == 0 and abs(result["actual_started_rps_before_signal"] / rate - 1) <= 0.1 and inflight["active"] > 0
            # Nonstream requests can be accepted before TERM and get EOF only
            # when a forced drain timeout closes the process. Their start time
            # alone is not evidence of a transport failure before the signal.
            result["refused_before_signal"] = sum(row["outcome"] == "connection_error" and row["end_ms"] < load["signal_at_ms"] for row in load["requests"])
            result["active_nonstream_connections_closed"] = sum(row["outcome"] == "connection_error" and not row["stream"] and row["start_ms"] < load["signal_at_ms"] <= row["end_ms"] for row in load["requests"])
            result["scenario_observed"] = (stopping["exit_code"] == 137 and not result["shutdown_flush_logged"]) if case == "kill" else (stopping["exit_code"] == 0 and (result["shutdown_timeout_logged"] and load["interrupted"] > 0 if long else not result["shutdown_timeout_logged"]))
            result["passed"] = result["scenario_observed"] and result["refused_before_signal"] == 0 and result["load_valid"] and result["accounting"]["passed"] and result["restart"]["passed"] and result["cache_after_stop"]["matches_database"] and load["http_error"] == 0 and (long or case == "kill" or load["interrupted"] == 0)
            if case.startswith("preconsume"):
                result["passed"] = result["passed"] and result["preconsume_observed"]
            (stage/"restart-gateway.log").write_text(docker("logs","--timestamps",restarted),encoding="utf-8")
            (stage/"result.json").write_text(json.dumps(result,indent=2),encoding="utf-8")
            results.append(result)
            print(json.dumps(result),flush=True)
    finally:
        summary = {"task_prefix":prefix,"database":args.database,"production_binary_sha256":binary_sha256,"elapsed_seconds":round(time.time()-started_wall,2),"passed":len(results)==len(args.cases.split(",")) and all(result["passed"] for result in results),"results":results}
        (root/"summary.json").write_text(json.dumps(summary,indent=2),encoding="utf-8")
        for child in children:
            if child.poll() is None:
                child.terminate()
                child.wait(timeout=10)
        for name in reversed(resources): docker("rm","-f",name,check=False)
        docker("network","rm",network,check=False)
    if not summary["passed"]:
        sys.exit(1)


if __name__ == "__main__":
    main()
